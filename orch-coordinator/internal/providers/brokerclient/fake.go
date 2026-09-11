package brokerclient

import (
	"context"
	"sync"

	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/types"
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
	// The settlement-keys fixture has its own error so a test can make
	// one endpoint fail without the others following.
	settlementKeys    *types.BrokerSettlementKeys
	settlementKeysErr error
	settlementKeysSet bool
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

// SetSettlementKeys installs a settlement-keys fixture for the given
// baseURL. Unset means the broker predates the endpoint.
func (f *FakeClient) SetSettlementKeys(baseURL string, out *types.BrokerSettlementKeys, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ent := f.results[baseURL]
	ent.settlementKeys = out
	ent.settlementKeysErr = err
	ent.settlementKeysSet = true
	f.results[baseURL] = ent
}

// FetchSettlementKeys satisfies the Client interface.
func (f *FakeClient) FetchSettlementKeys(ctx context.Context, baseURL string) (*types.BrokerSettlementKeys, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.results[baseURL]
	if !ok {
		return nil, ErrBrokerUnreachable
	}
	if !r.settlementKeysSet {
		return nil, ErrNotSupported
	}
	return r.settlementKeys, r.settlementKeysErr
}
