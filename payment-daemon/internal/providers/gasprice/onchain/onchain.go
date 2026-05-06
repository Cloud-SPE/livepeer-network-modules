// Package onchain is the chain-backed implementation of
// providers.GasPrice. Polls eth_gasPrice on a configurable interval and
// applies the operator-tuned multiplier (default 200% — 2× headroom
// over base-fee per the runbook).
package onchain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ethereum/go-ethereum/ethclient"
)

// Config holds the parameters for a GasPrice instance.
type Config struct {
	// MultiplierPct is applied as (rawGasPrice * MultiplierPct) / 100.
	// Zero defaults to 200 (2× headroom).
	MultiplierPct uint64

	// RefreshInterval is the cadence of eth_gasPrice polling. Zero
	// defaults to 5s.
	RefreshInterval time.Duration

	// Logger receives structured events. Nil = slog.Default().
	Logger *slog.Logger
}

// GasPrice is the chain-backed providers.GasPrice.
type GasPrice struct {
	cfg    Config
	client *ethclient.Client
	log    *slog.Logger

	current atomic.Pointer[big.Int]

	mu   sync.Mutex
	stop chan struct{}
	wg   sync.WaitGroup
}

// New constructs a GasPrice and runs an initial sync. Start runs the
// refresh goroutine.
func New(ctx context.Context, cfg Config, client *ethclient.Client) (*GasPrice, error) {
	if client == nil {
		return nil, errors.New("onchain gasprice: nil ethclient")
	}
	if cfg.MultiplierPct == 0 {
		cfg.MultiplierPct = 200
	}
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = 5 * time.Second
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	g := &GasPrice{
		cfg:    cfg,
		client: client,
		log:    logger.With("component", "onchain-gasprice"),
		stop:   make(chan struct{}),
	}
	if err := g.refresh(ctx); err != nil {
		return nil, fmt.Errorf("initial sync: %w", err)
	}
	return g, nil
}

// Start runs the refresh goroutine until Stop is called or the context
// passed to Start is cancelled.
func (g *GasPrice) Start(ctx context.Context) {
	g.wg.Add(1)
	go g.refreshLoop(ctx)
}

// Stop signals the refresh goroutine to exit and waits for it.
func (g *GasPrice) Stop() {
	g.mu.Lock()
	select {
	case <-g.stop:
		g.mu.Unlock()
		return
	default:
		close(g.stop)
	}
	g.mu.Unlock()
	g.wg.Wait()
}

// Current implements providers.GasPrice. Returns the most-recent
// observed gas price, already multiplied by the configured multiplier.
func (g *GasPrice) Current() *big.Int {
	if v := g.current.Load(); v != nil {
		return new(big.Int).Set(v)
	}
	return new(big.Int)
}

func (g *GasPrice) refreshLoop(ctx context.Context) {
	defer g.wg.Done()
	t := time.NewTicker(g.cfg.RefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-g.stop:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			if err := g.refresh(rctx); err != nil {
				g.log.Warn("gasprice refresh failed", "err", err)
			}
			cancel()
		}
	}
}

func (g *GasPrice) refresh(ctx context.Context) error {
	raw, err := g.client.SuggestGasPrice(ctx)
	if err != nil {
		return fmt.Errorf("eth_gasPrice: %w", err)
	}
	if raw == nil || raw.Sign() <= 0 {
		return errors.New("eth_gasPrice returned non-positive")
	}
	scaled := new(big.Int).Mul(raw, new(big.Int).SetUint64(g.cfg.MultiplierPct))
	scaled.Quo(scaled, big.NewInt(100))
	g.current.Store(scaled)
	return nil
}
