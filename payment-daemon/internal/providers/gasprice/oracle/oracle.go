// Package oracle is the chain-backed implementation of providers.GasPrice
// on top of chain-commons's gas oracle. It refreshes on a configurable
// interval, applies the operator-tuned multiplier (default 200% — 2×
// headroom over the chain's eth_gasPrice per the runbook), and serves
// the last good value from memory so the settlement hot path never
// waits on an RPC.
//
// The oracle's own TTL is set to the refresh interval, so one refresh is
// one eth_gasPrice call and the value the runbook documents is exactly
//
//	submitted_gas_price = eth_gasPrice × multiplier_pct / 100
package oracle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/gasoracle"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/gasoracle/ttl"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/rpc"
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
	oracle gasoracle.GasOracle
	log    *slog.Logger

	current atomic.Pointer[big.Int]

	mu   sync.Mutex
	stop chan struct{}
	wg   sync.WaitGroup
}

// New builds a GasPrice over a chain-commons TTL gas oracle on client
// and runs the initial sync. Start runs the refresh goroutine.
func New(ctx context.Context, cfg Config, client rpc.RPC) (*GasPrice, error) {
	if client == nil {
		return nil, errors.New("gasprice: nil rpc client")
	}
	applyDefaults(&cfg)
	o, err := ttl.New(ttl.Options{RPC: client, TTL: cfg.RefreshInterval})
	if err != nil {
		return nil, fmt.Errorf("gasprice: %w", err)
	}
	return NewWithOracle(ctx, cfg, o)
}

// NewWithOracle is New over an already-built oracle (tests, or a daemon
// that shares one oracle between consumers).
func NewWithOracle(ctx context.Context, cfg Config, o gasoracle.GasOracle) (*GasPrice, error) {
	if o == nil {
		return nil, errors.New("gasprice: nil oracle")
	}
	applyDefaults(&cfg)
	g := &GasPrice{
		cfg:    cfg,
		oracle: o,
		log:    cfg.Logger.With("component", "gasprice"),
		stop:   make(chan struct{}),
	}
	if err := g.refresh(ctx); err != nil {
		return nil, fmt.Errorf("initial sync: %w", err)
	}
	return g, nil
}

func applyDefaults(cfg *Config) {
	if cfg.MultiplierPct == 0 {
		cfg.MultiplierPct = 200
	}
	if cfg.RefreshInterval <= 0 {
		cfg.RefreshInterval = 5 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
}

// Start runs the refresh goroutine until Stop is called or the context
// passed to Start is cancelled.
func (g *GasPrice) Start(ctx context.Context) {
	g.wg.Add(1)
	go g.refreshLoop(ctx)
}

// Stop signals the refresh goroutine to exit and waits for it. Safe to
// call more than once.
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

// refresh reads the oracle once and stores the multiplied value. A
// failure leaves the last good value in place.
func (g *GasPrice) refresh(ctx context.Context) error {
	est, err := g.oracle.Suggest(ctx)
	if err != nil {
		return fmt.Errorf("eth_gasPrice: %w", err)
	}
	raw := est.BaseFee
	if raw == nil || raw.Sign() <= 0 {
		return errors.New("eth_gasPrice returned non-positive")
	}
	scaled := new(big.Int).Mul(raw, new(big.Int).SetUint64(g.cfg.MultiplierPct))
	scaled.Quo(scaled, big.NewInt(100))
	g.current.Store(scaled)
	return nil
}
