package diagnostics

import (
	"context"
	"errors"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/manifestfetcher"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

type chainStub struct{ err error }

func (c *chainStub) GetServiceURI(context.Context, types.EthAddress) (string, error) {
	return "", c.err
}
func TestProviderTransitions(t *testing.T) {
	state := New(true, true)
	if s := state.Snapshot(); s.ChainOK || s.ManifestFetcherOK || !s.LastChainSuccess.IsZero() {
		t.Fatal(s)
	}
	inner := &chainStub{err: types.ErrNotFound}
	c := state.WrapChain(inner)
	_, _ = c.GetServiceURI(context.Background(), "")
	healthy := state.Snapshot()
	if !healthy.ChainOK || healthy.LastChainSuccess.IsZero() {
		t.Fatal(healthy)
	}
	inner.err = errors.New("offline")
	_, _ = c.GetServiceURI(context.Background(), "")
	failed := state.Snapshot()
	if failed.ChainOK || !failed.LastChainSuccess.Equal(healthy.LastChainSuccess) {
		t.Fatal(failed)
	}
	inner.err = nil
	_, _ = c.GetServiceURI(context.Background(), "")
	if !state.Snapshot().ChainOK {
		t.Fatal("no recovery")
	}
	f := &manifestfetcher.Static{Bodies: map[string][]byte{"https://example.com": []byte("body")}}
	observed := state.WrapFetcher(f)
	_, _ = observed.Fetch(context.Background(), "missing")
	if state.Snapshot().ManifestFetcherOK {
		t.Fatal("failed fetch healthy")
	}
	_, _ = observed.Fetch(context.Background(), "https://example.com")
	if !state.Snapshot().ManifestFetcherOK {
		t.Fatal("no fetch recovery")
	}
	if s := New(false, false).Snapshot(); !s.ChainOK || !s.ManifestFetcherOK || !s.LastChainSuccess.IsZero() {
		t.Fatal(s)
	}
}
