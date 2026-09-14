// Package diagnostics records provider observations independently of metrics.
package diagnostics

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/manifestfetcher"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

type Snapshot struct {
	ChainOK, ManifestFetcherOK bool
	LastChainSuccess           time.Time
}
type State struct {
	mu       sync.RWMutex
	snapshot Snapshot
}

func New(chainRequired, fetchRequired bool) *State {
	return &State{snapshot: Snapshot{ChainOK: !chainRequired, ManifestFetcherOK: !fetchRequired}}
}
func (s *State) Snapshot() Snapshot                  { s.mu.RLock(); defer s.mu.RUnlock(); return s.snapshot }
func (s *State) WrapChain(c chain.Chain) chain.Chain { return &observedChain{inner: c, state: s} }
func (s *State) WrapFetcher(f manifestfetcher.ManifestFetcher) manifestfetcher.ManifestFetcher {
	return &observedFetcher{inner: f, state: s}
}

type observedChain struct {
	inner chain.Chain
	state *State
}

func (c *observedChain) GetServiceURI(ctx context.Context, addr types.EthAddress) (string, error) {
	uri, err := c.inner.GetServiceURI(ctx, addr)
	c.state.mu.Lock()
	defer c.state.mu.Unlock()
	c.state.snapshot.ChainOK = err == nil || errors.Is(err, types.ErrNotFound)
	if c.state.snapshot.ChainOK {
		c.state.snapshot.LastChainSuccess = time.Now().UTC()
	}
	return uri, err
}

type observedFetcher struct {
	inner manifestfetcher.ManifestFetcher
	state *State
}

func (f *observedFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	body, err := f.inner.Fetch(ctx, url)
	f.state.mu.Lock()
	defer f.state.mu.Unlock()
	f.state.snapshot.ManifestFetcherOK = err == nil
	return body, err
}
