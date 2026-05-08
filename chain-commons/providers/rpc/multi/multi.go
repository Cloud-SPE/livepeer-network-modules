// Package multi provides a multi-URL Ethereum RPC implementation with
// per-endpoint circuit breakers and primary/backup failover.
//
// See docs/design-docs/multi-rpc-failover.md for the full design.
package multi

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/config"
	cerrors "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/errors"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Options configures the multi-URL RPC.
type Options struct {
	// URLs is the ordered list of upstream RPC URLs. URLs[0] is the primary;
	// subsequent entries are backups. At least one URL is required.
	URLs []string

	// Policy is the retry / circuit-breaker tuning.
	Policy config.RPCPolicy

	// Clock injects time abstractions for tests. nil = clock.System().
	Clock clock.Clock

	// Logger is optional. nil = silent.
	Logger logger.Logger

	// Metrics is optional. nil = no-op.
	Metrics metrics.Recorder
}

// circuitState mirrors a classic three-state breaker.
type circuitState int32

const (
	circuitClosed   circuitState = 0
	circuitHalfOpen circuitState = 1
	circuitOpen     circuitState = 2
)

func (c circuitState) String() string {
	switch c {
	case circuitClosed:
		return "closed"
	case circuitHalfOpen:
		return "half-open"
	case circuitOpen:
		return "open"
	}
	return "unknown"
}

type endpoint struct {
	url    string
	role   string // "primary" | "backup"
	client *ethclient.Client

	// Per-endpoint state. Mutated under mu.
	mu                  sync.Mutex
	state               atomic.Int32 // circuitState
	consecutiveFailures int
	lastSuccess         time.Time
	lastFailure         time.Time
	openedAt            time.Time
	probeRunning        bool
}

// MultiRPC is the multi-URL go-ethereum wrapper that satisfies rpc.RPC.
type MultiRPC struct {
	endpoints []*endpoint
	policy    config.RPCPolicy
	clock     clock.Clock
	logger    logger.Logger
	metrics   metrics.Recorder

	stop  chan struct{}
	wg    sync.WaitGroup
}

// Open dials each configured URL and returns a MultiRPC. Returns an error
// if no URLs are configured or if every URL fails to dial.
func Open(opts Options) (*MultiRPC, error) {
	if len(opts.URLs) == 0 {
		return nil, errors.New("multi-rpc: at least one URL is required")
	}
	if opts.Clock == nil {
		opts.Clock = clock.System()
	}
	if opts.Metrics == nil {
		opts.Metrics = metrics.NoOp()
	}
	policy := opts.Policy
	applyDefaults(&policy)

	endpoints := make([]*endpoint, 0, len(opts.URLs))
	var dialErr error
	for i, url := range opts.URLs {
		client, err := ethclient.Dial(url)
		if err != nil {
			dialErr = fmt.Errorf("multi-rpc: dial %q: %w", url, err)
			continue
		}
		role := "backup"
		if i == 0 {
			role = "primary"
		}
		endpoints = append(endpoints, &endpoint{
			url:    url,
			role:   role,
			client: client,
		})
	}
	if len(endpoints) == 0 {
		return nil, dialErr
	}

	m := &MultiRPC{
		endpoints: endpoints,
		policy:    policy,
		clock:     opts.Clock,
		logger:    opts.Logger,
		metrics:   opts.Metrics,
		stop:      make(chan struct{}),
	}
	m.wg.Add(1)
	go m.healthProbeLoop()

	return m, nil
}

func applyDefaults(p *config.RPCPolicy) {
	if p.MaxRetries == 0 {
		p.MaxRetries = 6
	}
	if p.InitialBackoff == 0 {
		p.InitialBackoff = 500 * time.Millisecond
	}
	if p.BackoffFactor < 1 {
		p.BackoffFactor = 2.0
	}
	if p.MaxBackoff == 0 {
		p.MaxBackoff = 30 * time.Second
	}
	if p.HealthProbeInterval == 0 {
		p.HealthProbeInterval = 30 * time.Second
	}
	if p.CircuitBreakerThreshold == 0 {
		p.CircuitBreakerThreshold = 5
	}
	if p.CircuitBreakerCooloff == 0 {
		p.CircuitBreakerCooloff = 60 * time.Second
	}
	if p.CallTimeout == 0 {
		p.CallTimeout = 2 * time.Minute
	}
}

// Close releases all underlying connections and stops the health-probe loop.
func (m *MultiRPC) Close() error {
	close(m.stop)
	m.wg.Wait()
	for _, ep := range m.endpoints {
		ep.client.Close()
	}
	return nil
}

// Endpoints returns a snapshot of every endpoint's circuit state.
// Implements rpc.Inspector.
func (m *MultiRPC) Endpoints() []rpc.EndpointInfo {
	out := make([]rpc.EndpointInfo, 0, len(m.endpoints))
	for _, ep := range m.endpoints {
		ep.mu.Lock()
		out = append(out, rpc.EndpointInfo{
			URL:                 ep.url,
			Role:                ep.role,
			CircuitState:        circuitState(ep.state.Load()).String(),
			ConsecutiveFailures: ep.consecutiveFailures,
			LastSuccessUnix:     ep.lastSuccess.Unix(),
			LastFailureUnix:     ep.lastFailure.Unix(),
		})
		ep.mu.Unlock()
	}
	return out
}

// call routes fn through the multi-URL endpoints with retry + circuit
// breaker semantics. fn is invoked with the active *ethclient.Client.
func (m *MultiRPC) call(ctx context.Context, method string, fn func(*ethclient.Client) error) error {
	allOpen := true
	var lastErr error
	for _, ep := range m.endpoints {
		if circuitState(ep.state.Load()) == circuitOpen {
			continue
		}
		allOpen = false

		err := m.callWithRetry(ctx, ep, method, fn)
		if err == nil {
			return nil
		}
		lastErr = err
		// Permanent errors: surface immediately, do not failover.
		classified := cerrors.Classify(err)
		if classified.Class != cerrors.ClassTransient && classified.Class != cerrors.ClassUnknown {
			return classified
		}
		m.logf("rpc.endpoint_failed",
			logger.String("url", ep.url),
			logger.String("class", classified.Class.String()),
			logger.Err(err),
		)
		// Otherwise, try next endpoint.
	}
	if lastErr != nil && !allOpen {
		return cerrors.Wrap(cerrors.ClassTransient, "rpc.all_endpoints_failed",
			"every configured RPC endpoint failed for this call", lastErr)
	}
	if allOpen {
		return cerrors.New(cerrors.ClassCircuitOpen, "rpc.all_circuits_open",
			"every configured RPC endpoint has an open circuit")
	}
	return cerrors.New(cerrors.ClassTransient, "rpc.all_endpoints_failed",
		"every configured RPC endpoint failed for this call")
}

// callWithRetry runs fn against a single endpoint with exponential-backoff
// retry on transient errors, up to MaxRetries.
func (m *MultiRPC) callWithRetry(ctx context.Context, ep *endpoint, method string, fn func(*ethclient.Client) error) error {
	var lastErr error
	for attempt := 0; attempt <= m.policy.MaxRetries; attempt++ {
		// Per-call deadline only bounds fn; sleep uses the outer ctx so a
		// cancelled per-call ctx doesn't masquerade as success.
		_, cancel := context.WithTimeout(ctx, m.policy.CallTimeout)
		err := fn(ep.client)
		cancel()

		m.recordAttempt(ep, method, err)

		if err == nil {
			return nil
		}
		lastErr = err
		classified := cerrors.Classify(err)
		if classified.Class != cerrors.ClassTransient && classified.Class != cerrors.ClassUnknown {
			// Permanent — don't retry.
			return classified
		}
		if attempt >= m.policy.MaxRetries {
			break
		}
		// Sleep with exponential backoff against the outer ctx so external
		// cancellation propagates correctly.
		backoff := backoffDuration(m.policy, attempt)
		if err := m.clock.Sleep(ctx, backoff); err != nil {
			return err
		}
	}
	return lastErr
}

func backoffDuration(p config.RPCPolicy, attempt int) time.Duration {
	mult := math.Pow(p.BackoffFactor, float64(attempt))
	dur := time.Duration(float64(p.InitialBackoff) * mult)
	if dur > p.MaxBackoff {
		dur = p.MaxBackoff
	}
	return dur
}

// recordAttempt updates per-endpoint state and circuit breaker on each
// attempt outcome.
func (m *MultiRPC) recordAttempt(ep *endpoint, method string, err error) {
	ep.mu.Lock()
	defer ep.mu.Unlock()

	now := m.clock.Now()
	if err == nil {
		ep.consecutiveFailures = 0
		ep.lastSuccess = now
		// Half-open success closes the circuit.
		if circuitState(ep.state.Load()) == circuitHalfOpen {
			ep.state.Store(int32(circuitClosed))
			m.logf("rpc.circuit_recovered", logger.String("url", ep.url))
			m.metricsCounter("livepeer_chain_rpc_circuit_transitions_total",
				metrics.Labels{"endpoint": ep.url, "from": "half-open", "to": "closed"})
		}
		m.metricsCounter("livepeer_chain_rpc_calls_total",
			metrics.Labels{"endpoint": ep.url, "method": method, "outcome": "success"})
		return
	}

	classified := cerrors.Classify(err)
	if classified.Class != cerrors.ClassTransient && classified.Class != cerrors.ClassUnknown {
		// Don't penalize the endpoint for permanent errors.
		m.metricsCounter("livepeer_chain_rpc_calls_total",
			metrics.Labels{"endpoint": ep.url, "method": method, "outcome": "permanent"})
		return
	}

	ep.consecutiveFailures++
	ep.lastFailure = now
	m.metricsCounter("livepeer_chain_rpc_calls_total",
		metrics.Labels{"endpoint": ep.url, "method": method, "outcome": "transient"})

	if ep.consecutiveFailures >= m.policy.CircuitBreakerThreshold && circuitState(ep.state.Load()) != circuitOpen {
		from := circuitState(ep.state.Load())
		ep.state.Store(int32(circuitOpen))
		ep.openedAt = now
		m.logf("rpc.circuit_opened",
			logger.String("url", ep.url),
			logger.Int("consecutive_failures", ep.consecutiveFailures),
		)
		m.metricsCounter("livepeer_chain_rpc_circuit_transitions_total",
			metrics.Labels{"endpoint": ep.url, "from": from.String(), "to": "open"})
	}
}

func (m *MultiRPC) healthProbeLoop() {
	defer m.wg.Done()
	t := m.clock.NewTicker(m.policy.HealthProbeInterval)
	defer t.Stop()

	for {
		select {
		case <-m.stop:
			return
		case <-t.C():
			m.probeAll()
		}
	}
}

func (m *MultiRPC) probeAll() {
	for _, ep := range m.endpoints {
		ep.mu.Lock()
		if circuitState(ep.state.Load()) != circuitOpen || ep.probeRunning {
			ep.mu.Unlock()
			continue
		}
		now := m.clock.Now()
		if now.Sub(ep.openedAt) < m.policy.CircuitBreakerCooloff {
			ep.mu.Unlock()
			continue
		}
		ep.probeRunning = true
		ep.state.Store(int32(circuitHalfOpen))
		from := "open"
		ep.mu.Unlock()
		m.metricsCounter("livepeer_chain_rpc_circuit_transitions_total",
			metrics.Labels{"endpoint": ep.url, "from": from, "to": "half-open"})

		go func(ep *endpoint) {
			defer func() {
				ep.mu.Lock()
				ep.probeRunning = false
				ep.mu.Unlock()
			}()
			ctx, cancel := context.WithTimeout(context.Background(), m.policy.CallTimeout)
			defer cancel()
			_, err := ep.client.ChainID(ctx)
			m.recordAttempt(ep, "ChainID", err)
			if err != nil {
				// Probe failure: re-open the circuit.
				ep.mu.Lock()
				if circuitState(ep.state.Load()) != circuitOpen {
					ep.state.Store(int32(circuitOpen))
					ep.openedAt = m.clock.Now()
				}
				ep.mu.Unlock()
				m.logf("rpc.probe_failed", logger.String("url", ep.url), logger.Err(err))
				m.metricsCounter("livepeer_chain_rpc_circuit_transitions_total",
					metrics.Labels{"endpoint": ep.url, "from": "half-open", "to": "open"})
			}
		}(ep)
	}
}

func (m *MultiRPC) logf(msg string, fields ...logger.Field) {
	if m.logger == nil {
		return
	}
	m.logger.Info(msg, fields...)
}

func (m *MultiRPC) metricsCounter(name string, labels metrics.Labels) {
	if m.metrics == nil {
		return
	}
	m.metrics.CounterAdd(name, labels, 1)
}

// ----- rpc.RPC implementation: each method is a thin wrapper around call(). -----

func (m *MultiRPC) CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error) {
	var out []byte
	err := m.call(ctx, "CallContract", func(c *ethclient.Client) error {
		var err error
		out, err = c.CallContract(ctx, msg, blockNumber)
		return err
	})
	return out, err
}

func (m *MultiRPC) PendingCallContract(ctx context.Context, msg ethereum.CallMsg) ([]byte, error) {
	var out []byte
	err := m.call(ctx, "PendingCallContract", func(c *ethclient.Client) error {
		var err error
		out, err = c.PendingCallContract(ctx, msg)
		return err
	})
	return out, err
}

func (m *MultiRPC) CodeAt(ctx context.Context, addr chain.Address, blockNumber *big.Int) ([]byte, error) {
	var out []byte
	err := m.call(ctx, "CodeAt", func(c *ethclient.Client) error {
		var err error
		out, err = c.CodeAt(ctx, addr, blockNumber)
		return err
	})
	return out, err
}

func (m *MultiRPC) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	return m.call(ctx, "SendTransaction", func(c *ethclient.Client) error {
		return c.SendTransaction(ctx, tx)
	})
}

func (m *MultiRPC) TransactionByHash(ctx context.Context, hash chain.TxHash) (*types.Transaction, bool, error) {
	var (
		tx        *types.Transaction
		isPending bool
	)
	err := m.call(ctx, "TransactionByHash", func(c *ethclient.Client) error {
		var err error
		tx, isPending, err = c.TransactionByHash(ctx, hash)
		return err
	})
	return tx, isPending, err
}

func (m *MultiRPC) TransactionReceipt(ctx context.Context, hash chain.TxHash) (*types.Receipt, error) {
	var out *types.Receipt
	err := m.call(ctx, "TransactionReceipt", func(c *ethclient.Client) error {
		var err error
		out, err = c.TransactionReceipt(ctx, hash)
		return err
	})
	return out, err
}

func (m *MultiRPC) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	var out *types.Block
	err := m.call(ctx, "BlockByNumber", func(c *ethclient.Client) error {
		var err error
		out, err = c.BlockByNumber(ctx, number)
		return err
	})
	return out, err
}

func (m *MultiRPC) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	var out *types.Header
	err := m.call(ctx, "HeaderByNumber", func(c *ethclient.Client) error {
		var err error
		out, err = c.HeaderByNumber(ctx, number)
		return err
	})
	return out, err
}

func (m *MultiRPC) FilterLogs(ctx context.Context, query ethereum.FilterQuery) ([]types.Log, error) {
	var out []types.Log
	err := m.call(ctx, "FilterLogs", func(c *ethclient.Client) error {
		var err error
		out, err = c.FilterLogs(ctx, query)
		return err
	})
	return out, err
}

func (m *MultiRPC) PendingNonceAt(ctx context.Context, addr chain.Address) (uint64, error) {
	var out uint64
	err := m.call(ctx, "PendingNonceAt", func(c *ethclient.Client) error {
		var err error
		out, err = c.PendingNonceAt(ctx, addr)
		return err
	})
	return out, err
}

func (m *MultiRPC) BalanceAt(ctx context.Context, addr chain.Address, blockNumber *big.Int) (*big.Int, error) {
	var out *big.Int
	err := m.call(ctx, "BalanceAt", func(c *ethclient.Client) error {
		var err error
		out, err = c.BalanceAt(ctx, addr, blockNumber)
		return err
	})
	return out, err
}

func (m *MultiRPC) SuggestGasPrice(ctx context.Context) (*big.Int, error) {
	var out *big.Int
	err := m.call(ctx, "SuggestGasPrice", func(c *ethclient.Client) error {
		var err error
		out, err = c.SuggestGasPrice(ctx)
		return err
	})
	return out, err
}

func (m *MultiRPC) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	var out *big.Int
	err := m.call(ctx, "SuggestGasTipCap", func(c *ethclient.Client) error {
		var err error
		out, err = c.SuggestGasTipCap(ctx)
		return err
	})
	return out, err
}

func (m *MultiRPC) ChainID(ctx context.Context) (chain.ChainID, error) {
	var out *big.Int
	err := m.call(ctx, "ChainID", func(c *ethclient.Client) error {
		var err error
		out, err = c.ChainID(ctx)
		return err
	})
	if err != nil {
		return 0, err
	}
	if out == nil {
		// Defensive: should never happen since call() returned nil err only on
		// successful inner closure invocation — but guard so a future regression
		// doesn't surface as a nil-pointer panic.
		return 0, cerrors.New(cerrors.ClassTransient, "rpc.chainid_empty",
			"ChainID returned nil despite no error")
	}
	return chain.ChainID(out.Uint64()), nil
}

// Compile-time: MultiRPC satisfies rpc.RPC and rpc.Inspector.
var (
	_ rpc.RPC       = (*MultiRPC)(nil)
	_ rpc.Inspector = (*MultiRPC)(nil)
)
