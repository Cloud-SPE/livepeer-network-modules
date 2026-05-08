// Package poller provides a polling-based timesource.TimeSource that wraps
// providers/rpc.RPC. Polls RoundsManager.currentRound() and the latest block
// header on Config.BlockPollInterval, emits typed Round events when the
// round number changes, and L1 block events when the L2 header's
// l1BlockNumber field advances.
package poller

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/controller"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/timesource"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/crypto"
)

// currentRoundSelector is keccak256("currentRound()")[:4].
var currentRoundSelector = crypto.Keccak256([]byte("currentRound()"))[:4]

// roundLengthSelector is keccak256("roundLength()")[:4].
var roundLengthSelector = crypto.Keccak256([]byte("roundLength()"))[:4]

// currentRoundStartBlockSelector is keccak256("currentRoundStartBlock()")[:4].
var currentRoundStartBlockSelector = crypto.Keccak256([]byte("currentRoundStartBlock()"))[:4]

// lastInitializedRoundSelector is keccak256("lastInitializedRound()")[:4].
// Populates chain.Round.LastInitialized; consumers needing a round whose
// blockHashForRound() is non-zero (e.g., payment-daemon ticket creation)
// read this field rather than Number.
var lastInitializedRoundSelector = crypto.Keccak256([]byte("lastInitializedRound()"))[:4]

// currentRoundInitializedSelector is keccak256("currentRoundInitialized()")[:4].
// Populates chain.Round.Initialized.
var currentRoundInitializedSelector = crypto.Keccak256([]byte("currentRoundInitialized()"))[:4]

// Options wires the polling timesource.
type Options struct {
	RPC          rpc.RPC
	Controller   controller.Controller
	PollInterval time.Duration
	Clock        clock.Clock
	Logger       logger.Logger
}

// New constructs a polling timesource. RPC + Controller are required.
// PollInterval defaults to 5s.
func New(opts Options) (timesource.TimeSource, error) {
	if opts.RPC == nil {
		return nil, errors.New("timesource-poller: RPC is required")
	}
	if opts.Controller == nil {
		return nil, errors.New("timesource-poller: Controller is required")
	}
	if opts.PollInterval == 0 {
		opts.PollInterval = 5 * time.Second
	}
	if opts.Clock == nil {
		opts.Clock = clock.System()
	}

	t := &pollerSource{
		rpc:    opts.RPC,
		ctrl:   opts.Controller,
		poll:   opts.PollInterval,
		clock:  opts.Clock,
		logger: opts.Logger,
		stop:   make(chan struct{}),
	}
	t.rootCtx, t.rootCancel = context.WithCancel(context.Background())
	t.wg.Add(1)
	go t.loop()
	return t, nil
}

type pollerSource struct {
	rpc        rpc.RPC
	ctrl       controller.Controller
	poll       time.Duration
	clock      clock.Clock
	logger     logger.Logger
	rootCtx    context.Context
	rootCancel context.CancelFunc

	mu        sync.Mutex
	current   chain.Round
	roundSubs []chan chain.Round
	l1Subs    []chan chain.BlockNumber

	stop chan struct{}
	wg   sync.WaitGroup
}

func (t *pollerSource) CurrentRound(ctx context.Context) (chain.Round, error) {
	return t.fetchRound(ctx)
}

func (t *pollerSource) CurrentL1Block(ctx context.Context) (chain.BlockNumber, error) {
	bn, err := t.fetchL1Block(ctx)
	return bn, err
}

func (t *pollerSource) SubscribeRounds(_ context.Context) (<-chan chain.Round, error) {
	ch := make(chan chain.Round, 4)
	t.mu.Lock()
	t.roundSubs = append(t.roundSubs, ch)
	cur := t.current
	t.mu.Unlock()
	if cur.Number != 0 {
		select {
		case ch <- cur:
		default:
		}
	}
	return ch, nil
}

func (t *pollerSource) SubscribeL1Blocks(_ context.Context) (<-chan chain.BlockNumber, error) {
	ch := make(chan chain.BlockNumber, 4)
	t.mu.Lock()
	t.l1Subs = append(t.l1Subs, ch)
	t.mu.Unlock()
	return ch, nil
}

// Close stops the polling loop and closes subscribers.
func (t *pollerSource) Close() error {
	close(t.stop)
	if t.rootCancel != nil {
		t.rootCancel()
	}
	t.wg.Wait()
	return nil
}

func (t *pollerSource) loop() {
	defer t.wg.Done()
	ticker := t.clock.NewTicker(t.poll)
	defer ticker.Stop()

	// Initial fetch on startup — without it, the first round/L1 events
	// wouldn't fire until the first ticker tick, leaving consumers
	// blocked on subscriptions for up to PollInterval (30s default).
	{
		ctx, cancel := context.WithTimeout(t.rootCtx, 30*time.Second)
		t.tickOnce(ctx)
		cancel()
	}

	for {
		select {
		case <-t.stop:
			return
		case <-ticker.C():
			ctx, cancel := context.WithTimeout(t.rootCtx, 30*time.Second)
			t.tickOnce(ctx)
			cancel()
		}
	}
}

func (t *pollerSource) tickOnce(ctx context.Context) {
	round, err := t.fetchRound(ctx)
	if err == nil {
		t.maybeEmitRound(round)
	} else if t.logger != nil {
		t.logger.Warn("timesource.poll_round_failed", logger.Err(err))
	}
	bn, err := t.fetchL1Block(ctx)
	if err == nil {
		t.emitL1Block(bn)
	} else if t.logger != nil {
		t.logger.Warn("timesource.poll_l1_failed", logger.Err(err))
	}
}

func (t *pollerSource) fetchRound(ctx context.Context) (chain.Round, error) {
	rm := t.ctrl.Addresses().RoundsManager
	if rm == (chain.Address{}) {
		return chain.Round{}, errors.New("RoundsManager address not yet resolved")
	}

	num, err := t.callUint(ctx, rm, currentRoundSelector)
	if err != nil {
		return chain.Round{}, fmt.Errorf("currentRound: %w", err)
	}
	length, err := t.callUint(ctx, rm, roundLengthSelector)
	if err != nil {
		return chain.Round{}, fmt.Errorf("roundLength: %w", err)
	}
	startBlock, err := t.callUint(ctx, rm, currentRoundStartBlockSelector)
	if err != nil {
		return chain.Round{}, fmt.Errorf("currentRoundStartBlock: %w", err)
	}
	lastInit, err := t.callUint(ctx, rm, lastInitializedRoundSelector)
	if err != nil {
		return chain.Round{}, fmt.Errorf("lastInitializedRound: %w", err)
	}
	initialized, err := t.callBool(ctx, rm, currentRoundInitializedSelector)
	if err != nil {
		return chain.Round{}, fmt.Errorf("currentRoundInitialized: %w", err)
	}

	return chain.Round{
		Number:          chain.RoundNumber(num),
		StartBlock:      chain.BlockNumber(startBlock),
		Length:          chain.BlockNumber(length),
		Initialized:     initialized,
		LastInitialized: chain.RoundNumber(lastInit),
	}, nil
}

func (t *pollerSource) fetchL1Block(ctx context.Context) (chain.BlockNumber, error) {
	header, err := t.rpc.HeaderByNumber(ctx, nil)
	if err != nil {
		return 0, err
	}
	// On Arbitrum the L2 block header contains an l1BlockNumber field; on
	// vanilla Ethereum it doesn't. We don't have direct access to the field
	// from go-ethereum's typed Header, so as a portable fallback we use
	// header.Number — Arbitrum-specific extraction can be added later via
	// a custom RPC.RawHeader call.
	return chain.BlockNumber(header.Number.Uint64()), nil
}

func (t *pollerSource) callUint(ctx context.Context, to chain.Address, selector []byte) (uint64, error) {
	addr := to
	out, err := t.rpc.CallContract(ctx, ethereum.CallMsg{
		To:   &addr,
		Data: selector,
	}, nil)
	if err != nil {
		return 0, err
	}
	if len(out) < 32 {
		return 0, fmt.Errorf("expected 32-byte return, got %d", len(out))
	}
	// uint256 is big-endian in the rightmost bytes.
	return binary.BigEndian.Uint64(out[24:32]), nil
}

// callBool decodes a Solidity bool — non-zero last byte = true.
func (t *pollerSource) callBool(ctx context.Context, to chain.Address, selector []byte) (bool, error) {
	addr := to
	out, err := t.rpc.CallContract(ctx, ethereum.CallMsg{
		To:   &addr,
		Data: selector,
	}, nil)
	if err != nil {
		return false, err
	}
	if len(out) < 32 {
		return false, fmt.Errorf("expected 32-byte return, got %d", len(out))
	}
	return out[31] != 0, nil
}

func (t *pollerSource) maybeEmitRound(r chain.Round) {
	t.mu.Lock()
	// Emit on either Number change (new round became current) or
	// LastInitialized change (the contract's initializeRound() landed for
	// the current round). Both are state transitions consumers act on.
	changed := r.Number != t.current.Number || r.LastInitialized != t.current.LastInitialized
	t.current = r
	subs := append([]chan chain.Round(nil), t.roundSubs...)
	t.mu.Unlock()
	if !changed && r.Number == 0 {
		return
	}
	for _, ch := range subs {
		select {
		case ch <- r:
		default:
		}
	}
}

func (t *pollerSource) emitL1Block(bn chain.BlockNumber) {
	t.mu.Lock()
	subs := append([]chan chain.BlockNumber(nil), t.l1Subs...)
	t.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- bn:
		default:
		}
	}
}

// Compile-time: pollerSource satisfies timesource.TimeSource.
var _ timesource.TimeSource = (*pollerSource)(nil)

// AbiEncodeUint exposes uint256 ABI encoding for tests that want to mock
// callContract responses for the polling selectors.
func AbiEncodeUint(v uint64) []byte {
	out := make([]byte, 32)
	new(big.Int).SetUint64(v).FillBytes(out)
	return out
}

// AbiEncodeBool exposes Solidity bool ABI encoding (32 bytes; last byte
// 0x01 for true, 0x00 for false). Used by test fixtures that mock the
// currentRoundInitialized selector response.
func AbiEncodeBool(v bool) []byte {
	out := make([]byte, 32)
	if v {
		out[31] = 1
	}
	return out
}
