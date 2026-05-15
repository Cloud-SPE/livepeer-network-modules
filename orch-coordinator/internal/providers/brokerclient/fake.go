package brokerclient

import (
	"context"
	"sync"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/types"
)

// FakeClient is a deterministic in-memory brokerclient used by --dev
// mode and unit tests. Set the per-baseURL response with Set().
type FakeClient struct {
	mu      sync.Mutex
	results map[string]fakeEntry
}

type fakeEntry struct {
	offerings *types.BrokerOfferings
	health    *types.BrokerHealth
	err       error
}

// NewFake returns an empty FakeClient. Caller must Set() each baseURL.
func NewFake() *FakeClient { return &FakeClient{results: make(map[string]fakeEntry)} }

// Set installs a fixture for the given baseURL. Either out or err may
// be nil but not both — a nil/nil pair is undefined.
func (f *FakeClient) Set(baseURL string, out *types.BrokerOfferings, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ent := f.results[baseURL]
	ent.offerings = out
	ent.err = err
	f.results[baseURL] = ent
}

// SetHealth installs a health fixture for the given baseURL.
func (f *FakeClient) SetHealth(baseURL string, out *types.BrokerHealth, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ent := f.results[baseURL]
	ent.health = out
	ent.err = err
	f.results[baseURL] = ent
}

// FetchOfferings satisfies the Client interface.
func (f *FakeClient) FetchOfferings(ctx context.Context, baseURL string) (*types.BrokerOfferings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.results[baseURL]; ok {
		return r.offerings, r.err
	}
	return nil, ErrBrokerUnreachable
}

// FetchHealth satisfies the Client interface.
func (f *FakeClient) FetchHealth(ctx context.Context, baseURL string) (*types.BrokerHealth, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.results[baseURL]; ok {
		if r.health != nil || r.err != nil {
			return r.health, r.err
		}
		return &types.BrokerHealth{}, nil
	}
	return nil, ErrBrokerUnreachable
}
