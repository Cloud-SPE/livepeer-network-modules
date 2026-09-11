// Package chain is the chain-backed implementation of providers.Clock on
// top of chain-commons (plan 0048 stage 5).
//
// chain-commons's polling timesource observes RoundsManager on
// --clock-refresh-interval and emits an event when the active or the
// last-initialized round changes, and one every poll with the head
// block. This clock subscribes through roundclock and, on each event,
// reads what the daemon needs through the shared contract bindings:
// blockHashForRound for the last-initialized round (cached per round,
// since a round's hash never changes once set) and
// BondingManager.getTranscoderPoolSize for the escrow's reserve-alloc
// math. The head block number rides along on the L1-block event.
//
// Every read goes through chain-commons rpc.RPC, so it fails over
// across --chain-rpc-urls like every other chain call. Poll-only, no
// eth_subscribe (plan 0016 Q5).
package chain

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	cchain "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/bondingmanager"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/controller"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/roundsmanager"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/rpc"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/timesource"
	tspoller "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/timesource/poller"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/roundclock"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/chaincommons"
)

// Config holds the parameters for a Clock instance.
type Config struct {
	// RefreshInterval is the timesource poll cadence. Default 30s.
	RefreshInterval time.Duration
	Logger          *slog.Logger
}

// Clock is the chain-backed providers.Clock.
type Clock struct {
	cfg     Config
	rpc     rpc.RPC
	ctrl    controller.Controller
	rounds  *roundsmanager.Bindings
	bonding *bondingmanager.Bindings
	log     *slog.Logger

	state    atomic.Pointer[clockState]
	poolSize atomic.Pointer[big.Int]

	mu        sync.Mutex
	hashCache map[cchain.RoundNumber]cchain.TxHash

	// newTimeSource builds the poller; tests swap it for a scripted
	// source to drive events without a ticker.
	newTimeSource func(ctx context.Context) (timesource.TimeSource, error)

	ts       timesource.TimeSource
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

type clockState struct {
	round      cchain.RoundNumber
	roundHash  cchain.TxHash
	l1BlockNum uint64
	updatedAt  time.Time
}

// New constructs a Clock and runs an initial sync through the contract
// bindings, so values are available before Start. ctrl supplies the
// RoundsManager and BondingManager addresses and is shared with the
// timesource, exactly as protocol-daemon wires it.
func New(ctx context.Context, cfg Config, r rpc.RPC, ctrl controller.Controller) (*Clock, error) {
	if r == nil {
		return nil, errors.New("chain clock: nil rpc client")
	}
	if ctrl == nil {
		return nil, errors.New("chain clock: nil controller")
	}
	addrs := ctrl.Addresses()
	if addrs.RoundsManager == (cchain.Address{}) {
		return nil, errors.New("chain clock: RoundsManager address not resolved")
	}
	if addrs.BondingManager == (cchain.Address{}) {
		return nil, errors.New("chain clock: BondingManager address not resolved")
	}
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = 30 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	rounds, err := roundsmanager.New(r, addrs.RoundsManager)
	if err != nil {
		return nil, fmt.Errorf("chain clock: %w", err)
	}
	bonding, err := bondingmanager.New(r, addrs.BondingManager)
	if err != nil {
		return nil, fmt.Errorf("chain clock: %w", err)
	}
	c := &Clock{
		cfg:       cfg,
		rpc:       r,
		ctrl:      ctrl,
		rounds:    rounds,
		bonding:   bonding,
		log:       logger.With("component", "chain-clock"),
		hashCache: map[cchain.RoundNumber]cchain.TxHash{},
		stop:      make(chan struct{}),
	}
	c.newTimeSource = func(context.Context) (timesource.TimeSource, error) {
		return tspoller.New(tspoller.Options{
			RPC:          r,
			Controller:   ctrl,
			PollInterval: cfg.RefreshInterval,
			Logger:       chaincommons.Logger(c.log.With("component", "timesource")),
		})
	}
	if err := c.initialSync(ctx); err != nil {
		return nil, fmt.Errorf("initial sync: %w", err)
	}
	return c, nil
}

// Start opens the timesource and follows its round and head-block
// events until Stop is called or ctx is cancelled.
func (c *Clock) Start(ctx context.Context) error {
	ts, err := c.newTimeSource(ctx)
	if err != nil {
		return fmt.Errorf("chain clock: timesource: %w", err)
	}
	rc, err := roundclock.New(roundclock.Options{
		TimeSource: ts,
		Logger:     chaincommons.Logger(c.log.With("component", "roundclock")),
	})
	if err != nil {
		closeTimeSource(ts)
		return fmt.Errorf("chain clock: roundclock: %w", err)
	}
	rounds, err := rc.SubscribeRounds(ctx)
	if err != nil {
		closeTimeSource(ts)
		return fmt.Errorf("chain clock: subscribe rounds: %w", err)
	}
	blocks, err := rc.SubscribeL1Blocks(ctx)
	if err != nil {
		closeTimeSource(ts)
		return fmt.Errorf("chain clock: subscribe blocks: %w", err)
	}
	c.ts = ts
	c.wg.Add(1)
	go c.follow(ctx, rounds, blocks)
	return nil
}

// Stop closes the timesource and waits for the follower to exit.
// Idempotent.
func (c *Clock) Stop() {
	c.stopOnce.Do(func() { close(c.stop) })
	c.wg.Wait()
	if c.ts != nil {
		closeTimeSource(c.ts)
		c.ts = nil
	}
}

func closeTimeSource(ts timesource.TimeSource) {
	if cl, ok := ts.(io.Closer); ok {
		_ = cl.Close()
	}
}

// LastInitializedRound implements providers.Clock.
func (c *Clock) LastInitializedRound() int64 {
	if s := c.state.Load(); s != nil {
		return int64(s.round)
	}
	return 0
}

// LastInitializedL1BlockHash implements providers.Clock.
func (c *Clock) LastInitializedL1BlockHash() []byte {
	if s := c.state.Load(); s != nil {
		return append([]byte(nil), s.roundHash[:]...)
	}
	return nil
}

// LastSeenL1Block implements providers.Clock.
func (c *Clock) LastSeenL1Block() *big.Int {
	if s := c.state.Load(); s != nil {
		return new(big.Int).SetUint64(s.l1BlockNum)
	}
	return new(big.Int)
}

// GetTranscoderPoolSize implements providers.Clock.
func (c *Clock) GetTranscoderPoolSize() *big.Int {
	if v := c.poolSize.Load(); v != nil {
		return new(big.Int).Set(v)
	}
	return new(big.Int)
}

// initialSync reads everything once through the bindings so the clock
// is usable before the first timesource event, and so a dead chain is
// a construction error rather than a silent zero clock.
func (c *Clock) initialSync(ctx context.Context) error {
	round, err := c.rounds.LastInitializedRound(ctx)
	if err != nil {
		return fmt.Errorf("lastInitializedRound: %w", err)
	}
	hash, err := c.blockHashForRound(ctx, round)
	if err != nil {
		return err
	}
	if err := c.refreshPoolSize(ctx); err != nil {
		return err
	}
	head, err := c.rpc.HeaderByNumber(ctx, nil)
	if err != nil {
		return fmt.Errorf("head header: %w", err)
	}
	if head == nil || head.Number == nil {
		return errors.New("head header: empty")
	}
	c.state.Store(&clockState{
		round:      round,
		roundHash:  hash,
		l1BlockNum: head.Number.Uint64(),
		updatedAt:  time.Now(),
	})
	return nil
}

func (c *Clock) follow(ctx context.Context, rounds <-chan cchain.Round, blocks <-chan cchain.BlockNumber) {
	defer c.wg.Done()
	for {
		select {
		case <-c.stop:
			return
		case <-ctx.Done():
			return
		case r, ok := <-rounds:
			if !ok {
				return
			}
			c.onRound(ctx, r)
		case bn, ok := <-blocks:
			if !ok {
				return
			}
			c.onBlock(ctx, bn)
		}
	}
}

// onRound follows a round transition: the event already carries
// lastInitializedRound (the poller reads it), so only the hash is
// fetched, and only when the round is new to the cache.
func (c *Clock) onRound(ctx context.Context, r cchain.Round) {
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	hash, err := c.blockHashForRound(rctx, r.LastInitialized)
	if err != nil {
		c.log.Warn("clock round refresh failed", "round", uint64(r.LastInitialized), "err", err)
		return
	}
	prev := c.state.Load()
	next := &clockState{round: r.LastInitialized, roundHash: hash, updatedAt: time.Now()}
	if prev != nil {
		next.l1BlockNum = prev.l1BlockNum
	}
	c.state.Store(next)
}

// onBlock follows a poll tick: records the head block and re-reads the
// transcoder pool size, which changes on any round boundary and on
// activations/deactivations in between.
func (c *Clock) onBlock(ctx context.Context, bn cchain.BlockNumber) {
	prev := c.state.Load()
	next := &clockState{l1BlockNum: uint64(bn), updatedAt: time.Now()}
	if prev != nil {
		next.round = prev.round
		next.roundHash = prev.roundHash
	}
	c.state.Store(next)

	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := c.refreshPoolSize(rctx); err != nil {
		c.log.Warn("clock pool-size refresh failed", "err", err)
	}
}

func (c *Clock) refreshPoolSize(ctx context.Context) error {
	n, err := c.bonding.GetTranscoderPoolSize(ctx)
	if err != nil {
		return fmt.Errorf("getTranscoderPoolSize: %w", err)
	}
	c.poolSize.Store(new(big.Int).SetUint64(n))
	return nil
}

// blockHashForRound is cached per round: rounds advance monotonically
// and a round's hash never changes once initialized, so the cache grows
// by one entry per round transition.
func (c *Clock) blockHashForRound(ctx context.Context, round cchain.RoundNumber) (cchain.TxHash, error) {
	c.mu.Lock()
	h, ok := c.hashCache[round]
	c.mu.Unlock()
	if ok {
		return h, nil
	}
	h, err := c.rounds.BlockHashForRound(ctx, round)
	if err != nil {
		return cchain.TxHash{}, fmt.Errorf("blockHashForRound(%d): %w", round, err)
	}
	c.mu.Lock()
	c.hashCache[round] = h
	c.mu.Unlock()
	return h, nil
}
