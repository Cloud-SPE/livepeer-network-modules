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
	out *types.BrokerOfferings
	err error
}

// NewFake returns an empty FakeClient. Caller must Set() each baseURL.
func NewFake() *FakeClient { return &FakeClient{results: make(map[string]fakeEntry)} }

// Set installs a fixture for the given baseURL. Either out or err may
// be nil but not both — a nil/nil pair is undefined.
func (f *FakeClient) Set(baseURL string, out *types.BrokerOfferings, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[baseURL] = fakeEntry{out: out, err: err}
}

// Fetch satisfies the Client interface.
func (f *FakeClient) Fetch(ctx context.Context, baseURL string) (*types.BrokerOfferings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.results[baseURL]; ok {
		return r.out, r.err
	}
	return nil, ErrBrokerUnreachable
}
