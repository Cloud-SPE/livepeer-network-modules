// Package onchain is the chain-backed implementation of providers.Clock.
//
// Polls RoundsManager.lastInitializedRound + blockHashForRound on a
// configurable interval; reports BondingManager.getTranscoderPoolSize
// for the escrow's reserve-alloc math; tracks the latest observed L1
// block via eth_blockNumber.
//
// Per Q5 the implementation is poll-only — no eth_subscribe / WSS
// reconnect logic. The default 30s refresh-interval is bounded enough
// for round/block staleness in the daemon hot path; tighten via flag
// when needed.
package onchain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

const roundsManagerABI = `[
  {"type":"function","name":"lastInitializedRound","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256"}]},
  {"type":"function","name":"blockHashForRound","stateMutability":"view","inputs":[{"name":"_round","type":"uint256"}],"outputs":[{"name":"","type":"bytes32"}]}
]`

const bondingManagerABI = `[
  {"type":"function","name":"getTranscoderPoolSize","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"uint256"}]}
]`

var roundsABI = mustParse(roundsManagerABI)
var bondingABI = mustParse(bondingManagerABI)

func mustParse(src string) abi.ABI {
	a, err := abi.JSON(strings.NewReader(src))
	if err != nil {
		panic("onchain clock: malformed ABI: " + err.Error())
	}
	return a
}

// Config holds the parameters for a Clock instance.
type Config struct {
	RoundsManager   ethcommon.Address
	BondingManager  ethcommon.Address
	RefreshInterval time.Duration
	Logger          *slog.Logger
}

// Clock is the chain-backed providers.Clock.
type Clock struct {
	cfg    Config
	client *ethclient.Client
	log    *slog.Logger

	state    atomic.Pointer[clockState]
	poolSize atomic.Pointer[big.Int]

	mu        sync.Mutex
	hashCache map[int64][]byte

	stop chan struct{}
	wg   sync.WaitGroup
}

type clockState struct {
	round       int64
	roundHash   []byte
	l1BlockNum  *big.Int
	updatedAt   time.Time
}

// New constructs a Clock and runs an initial sync. The refresh
// goroutine is started by Start; until Start runs, the values reflect
// the initial sync only.
func New(ctx context.Context, cfg Config, client *ethclient.Client) (*Clock, error) {
	if client == nil {
		return nil, errors.New("onchain clock: nil ethclient")
	}
	if (cfg.RoundsManager == ethcommon.Address{}) {
		return nil, errors.New("onchain clock: empty RoundsManager address")
	}
	if (cfg.BondingManager == ethcommon.Address{}) {
		return nil, errors.New("onchain clock: empty BondingManager address")
	}
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = 30 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	c := &Clock{
		cfg:       cfg,
		client:    client,
		log:       logger.With("component", "onchain-clock"),
		hashCache: map[int64][]byte{},
		stop:      make(chan struct{}),
	}
	if err := c.refresh(ctx); err != nil {
		return nil, fmt.Errorf("initial sync: %w", err)
	}
	return c, nil
}

// Start runs the refresh goroutine until Stop is called or the context
// passed to Start is cancelled.
func (c *Clock) Start(ctx context.Context) {
	c.wg.Add(1)
	go c.refreshLoop(ctx)
}

// Stop signals the refresh goroutine to exit and waits for it.
func (c *Clock) Stop() {
	select {
	case <-c.stop:
		return
	default:
		close(c.stop)
	}
	c.wg.Wait()
}

// LastInitializedRound implements providers.Clock.
func (c *Clock) LastInitializedRound() int64 {
	if s := c.state.Load(); s != nil {
		return s.round
	}
	return 0
}

// LastInitializedL1BlockHash implements providers.Clock.
func (c *Clock) LastInitializedL1BlockHash() []byte {
	if s := c.state.Load(); s != nil {
		return append([]byte(nil), s.roundHash...)
	}
	return nil
}

// LastSeenL1Block implements providers.Clock.
func (c *Clock) LastSeenL1Block() *big.Int {
	if s := c.state.Load(); s != nil && s.l1BlockNum != nil {
		return new(big.Int).Set(s.l1BlockNum)
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

func (c *Clock) refreshLoop(ctx context.Context) {
	defer c.wg.Done()
	t := time.NewTicker(c.cfg.RefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			if err := c.refresh(rctx); err != nil {
				c.log.Warn("clock refresh failed", "err", err)
			}
			cancel()
		}
	}
}

func (c *Clock) refresh(ctx context.Context) error {
	round, err := c.callLastInitRound(ctx)
	if err != nil {
		return fmt.Errorf("lastInitializedRound: %w", err)
	}
	hash, err := c.blockHashForRound(ctx, round)
	if err != nil {
		return fmt.Errorf("blockHashForRound(%d): %w", round, err)
	}
	pool, err := c.callPoolSize(ctx)
	if err != nil {
		return fmt.Errorf("getTranscoderPoolSize: %w", err)
	}
	head, err := c.client.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("blockNumber: %w", err)
	}

	c.poolSize.Store(pool)
	c.state.Store(&clockState{
		round:      round,
		roundHash:  hash,
		l1BlockNum: new(big.Int).SetUint64(head),
		updatedAt:  time.Now(),
	})
	return nil
}

func (c *Clock) callLastInitRound(ctx context.Context) (int64, error) {
	data, err := roundsABI.Pack("lastInitializedRound")
	if err != nil {
		return 0, err
	}
	out, err := c.client.CallContract(ctx, ethereum.CallMsg{To: &c.cfg.RoundsManager, Data: data}, nil)
	if err != nil {
		return 0, err
	}
	res, err := roundsABI.Unpack("lastInitializedRound", out)
	if err != nil {
		return 0, err
	}
	if len(res) != 1 {
		return 0, fmt.Errorf("expected 1 return value, got %d", len(res))
	}
	v, ok := res[0].(*big.Int)
	if !ok {
		return 0, fmt.Errorf("unexpected return type %T", res[0])
	}
	if !v.IsInt64() {
		return 0, fmt.Errorf("round %s overflows int64", v.String())
	}
	return v.Int64(), nil
}

// blockHashForRound looks up the L1 block hash associated with a round.
// Cached per-round; rounds advance monotonically so the cache only
// grows by one entry per round transition.
func (c *Clock) blockHashForRound(ctx context.Context, round int64) ([]byte, error) {
	c.mu.Lock()
	if h, ok := c.hashCache[round]; ok {
		c.mu.Unlock()
		return append([]byte(nil), h...), nil
	}
	c.mu.Unlock()

	data, err := roundsABI.Pack("blockHashForRound", big.NewInt(round))
	if err != nil {
		return nil, err
	}
	out, err := c.client.CallContract(ctx, ethereum.CallMsg{To: &c.cfg.RoundsManager, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	res, err := roundsABI.Unpack("blockHashForRound", out)
	if err != nil {
		return nil, err
	}
	if len(res) != 1 {
		return nil, fmt.Errorf("expected 1 return value, got %d", len(res))
	}
	h, ok := res[0].([32]byte)
	if !ok {
		return nil, fmt.Errorf("unexpected return type %T", res[0])
	}
	hashBytes := append([]byte(nil), h[:]...)
	c.mu.Lock()
	c.hashCache[round] = hashBytes
	c.mu.Unlock()
	return append([]byte(nil), hashBytes...), nil
}

func (c *Clock) callPoolSize(ctx context.Context) (*big.Int, error) {
	data, err := bondingABI.Pack("getTranscoderPoolSize")
	if err != nil {
		return nil, err
	}
	out, err := c.client.CallContract(ctx, ethereum.CallMsg{To: &c.cfg.BondingManager, Data: data}, nil)
	if err != nil {
		return nil, err
	}
	res, err := bondingABI.Unpack("getTranscoderPoolSize", out)
	if err != nil {
		return nil, err
	}
	if len(res) != 1 {
		return nil, fmt.Errorf("expected 1 return value, got %d", len(res))
	}
	v, ok := res[0].(*big.Int)
	if !ok {
		return nil, fmt.Errorf("unexpected return type %T", res[0])
	}
	return v, nil
}
