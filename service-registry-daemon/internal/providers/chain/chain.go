// Package chain defines the Chain provider — the abstraction service/
// uses to read the on-chain ServiceRegistry.serviceURI for an Ethereum
// address.
//
// Two production-relevant implementations:
//
//   - InMemory: zero-dependency map. Used by --dev mode, tests, and
//     the static-overlay-only example.
//   - Eth: go-ethereum-backed reader. Performs eth_call against the
//     ServiceRegistry contract using a hand-encoded ABI selector.
package chain

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

// WithMetrics wraps a Chain so every read also emits the corresponding
// metrics. The wrapper is thin and allocation-free per-call — the
// recorder methods are inlined by the compiler when the recorder is the
// Noop type.
func WithMetrics(c Chain, rec metrics.Recorder) Chain {
	if rec == nil {
		return c
	}
	return &meteredChain{inner: c, rec: rec}
}

type meteredChain struct {
	inner Chain
	rec   metrics.Recorder
}

func (m *meteredChain) GetServiceURI(ctx context.Context, addr types.EthAddress) (string, error) {
	start := time.Now()
	uri, err := m.inner.GetServiceURI(ctx, addr)
	m.rec.ObserveChainRead(time.Since(start))
	switch {
	case err == nil:
		m.rec.IncChainRead(metrics.OutcomeOK)
		m.rec.SetChainLastSuccess(time.Now())
	case errors.Is(err, types.ErrNotFound):
		m.rec.IncChainRead(metrics.OutcomeNotFound)
	default:
		m.rec.IncChainRead(metrics.OutcomeUnavailable)
	}
	return uri, err
}

// Chain reads the on-chain pointer that maps an Ethereum orchestrator
// address to its serviceURI string. ServiceURI is returned verbatim —
// interpretation lives in service/resolver.
type Chain interface {
	// GetServiceURI returns the on-chain serviceURI string for addr.
	// Returns types.ErrNotFound if the address has never had one set.
	GetServiceURI(ctx context.Context, addr types.EthAddress) (string, error)
}

// InMemory is a Chain implementation backed by a sync map. Used by
// dev mode, tests, and the static-overlay-only example.
type InMemory struct {
	mu sync.RWMutex
	// map[lower-cased-address] -> serviceURI string
	state map[types.EthAddress]string
}

// NewInMemory returns an empty in-memory chain.
func NewInMemory(_ types.EthAddress) *InMemory {
	return &InMemory{
		state: make(map[types.EthAddress]string),
	}
}

// PreLoad seeds (addr, uri) entries. Test/example helper; not part of
// the Chain interface.
func (m *InMemory) PreLoad(addr types.EthAddress, uri string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state[addr] = uri
}

// GetServiceURI returns the serviceURI for addr or ErrNotFound.
func (m *InMemory) GetServiceURI(_ context.Context, addr types.EthAddress) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	uri, ok := m.state[addr]
	if !ok {
		return "", types.ErrNotFound
	}
	return uri, nil
}
