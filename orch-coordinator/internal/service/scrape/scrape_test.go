package scrape

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/providers/brokerclient"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/types"
)

type fakeClient struct {
	mu      sync.Mutex
	results map[string]fakeResult
}

type fakeResult struct {
	out *types.BrokerOfferings
	err error
}

func (f *fakeClient) Fetch(ctx context.Context, baseURL string) (*types.BrokerOfferings, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.results[baseURL]; ok {
		return r.out, r.err
	}
	return nil, errors.New("fakeClient: no fixture for " + baseURL)
}

func (f *fakeClient) set(baseURL string, out *types.BrokerOfferings, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[baseURL] = fakeResult{out: out, err: err}
}

func newFake() *fakeClient {
	return &fakeClient{results: make(map[string]fakeResult)}
}

func newOfferings(addr string, caps ...types.BrokerOffering) *types.BrokerOfferings {
	return &types.BrokerOfferings{OrchEthAddress: addr, Capabilities: caps}
}

func sampleCap(id, off string) types.BrokerOffering {
	return types.BrokerOffering{
		CapabilityID:    id,
		OfferingID:      off,
		InteractionMode: "http-stream@v1",
		WorkUnit:        types.WorkUnit{Name: "tokens"},
		PricePerUnitWei: "100",
	}
}

func TestService_HappyPath(t *testing.T) {
	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fc := newFake()
	fc.set("http://x:1", newOfferings(addr, sampleCap("c", "o")), nil)
	svc, err := New(Config{
		OrchEthAddress: addr,
		Brokers:        []config.Broker{{Name: "b1", BaseURL: "http://x:1"}},
		ScrapeInterval: 50 * time.Millisecond,
		ScrapeTimeout:  100 * time.Millisecond,
	}, fc, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	svc.ScrapeOnce(context.Background())
	snap := svc.Snapshot()
	if len(snap.SourceTuples) != 1 {
		t.Fatalf("expected 1 source tuple, got %d", len(snap.SourceTuples))
	}
	if snap.Brokers[0].Freshness != FreshnessOK {
		t.Fatalf("freshness: %s", snap.Brokers[0].Freshness)
	}
}

func TestService_SoftFailureKeepsLastGood(t *testing.T) {
	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fc := newFake()
	fc.set("http://x:1", newOfferings(addr, sampleCap("c", "o")), nil)
	svc, _ := New(Config{
		OrchEthAddress: addr,
		Brokers:        []config.Broker{{Name: "b1", BaseURL: "http://x:1"}},
		ScrapeInterval: 50 * time.Millisecond,
		ScrapeTimeout:  100 * time.Millisecond,
	}, fc, slog.Default())
	svc.ScrapeOnce(context.Background())

	// Now switch broker to a soft failure.
	fc.set("http://x:1", nil, brokerclient.ErrBrokerUnreachable)
	svc.ScrapeOnce(context.Background())
	snap := svc.Snapshot()
	if snap.Brokers[0].Freshness != FreshnessStaleFailing {
		t.Fatalf("freshness: %s", snap.Brokers[0].Freshness)
	}
	if len(snap.Brokers[0].Offerings) != 1 {
		t.Fatalf("last-good entries dropped on soft fail")
	}
}

func TestService_HardFailureDropsEntries(t *testing.T) {
	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fc := newFake()
	fc.set("http://x:1", newOfferings(addr, sampleCap("c", "o")), nil)
	svc, _ := New(Config{
		OrchEthAddress: addr,
		Brokers:        []config.Broker{{Name: "b1", BaseURL: "http://x:1"}},
		ScrapeInterval: 50 * time.Millisecond,
		ScrapeTimeout:  100 * time.Millisecond,
	}, fc, slog.Default())
	svc.ScrapeOnce(context.Background())

	// Hard failure (schema).
	fc.set("http://x:1", nil, brokerclient.ErrBrokerSchema)
	svc.ScrapeOnce(context.Background())
	snap := svc.Snapshot()
	if snap.Brokers[0].Freshness != FreshnessSchemaError {
		t.Fatalf("freshness: %s", snap.Brokers[0].Freshness)
	}
	if len(snap.Brokers[0].Offerings) != 0 {
		t.Fatalf("hard fail must drop entries immediately")
	}
}

func TestService_OrchMismatchIsHardFail(t *testing.T) {
	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fc := newFake()
	fc.set("http://x:1", newOfferings("0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", sampleCap("c", "o")), nil)
	svc, _ := New(Config{
		OrchEthAddress: addr,
		Brokers:        []config.Broker{{Name: "b1", BaseURL: "http://x:1"}},
		ScrapeInterval: 50 * time.Millisecond,
		ScrapeTimeout:  100 * time.Millisecond,
	}, fc, slog.Default())
	svc.ScrapeOnce(context.Background())
	snap := svc.Snapshot()
	if snap.Brokers[0].Freshness != FreshnessSchemaError {
		t.Fatalf("freshness: %s", snap.Brokers[0].Freshness)
	}
}
