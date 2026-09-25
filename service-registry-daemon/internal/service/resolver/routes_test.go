package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/repo/manifestcache"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/selection"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

var testFilter = selection.Filter{Capability: "example:work", Offering: "default"}

const testBroker = "https://broker.example.com"

func routeFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t)
	f.signManifestForFixture([]types.Node{{ID: "broker", URL: testBroker, Capabilities: []types.Capability{{Name: testFilter.Capability, Protocol: "paid-job/v1", WorkUnit: "unit", Offerings: []types.Offering{{ID: testFilter.Offering, PricePerWorkUnitWei: "7"}}}}}})
	f.health.snapshots[testBroker] = &types.RouteHealthSnapshot{Capabilities: []types.RouteHealthCapability{{ID: testFilter.Capability, OfferingID: testFilter.Offering, Status: "ready", StaleAfter: f.clk.Now().Add(time.Minute)}}}
	f.svc.Discover([]types.EthAddress{f.addr})
	return f
}
func warmRoute(t *testing.T, f *fixture) {
	t.Helper()
	if err := f.svc.RefreshAddress(context.Background(), f.addr); err != nil {
		t.Fatal(err)
	}
	f.svc.RefreshHealth(context.Background(), testBroker)
}
func expectSelection(t *testing.T, s *Service, want error) {
	t.Helper()
	nodes, err := s.SelectNodes(context.Background(), testFilter)
	if want == nil {
		if err != nil || len(nodes) != 1 {
			t.Fatalf("nodes=%d err=%v", len(nodes), err)
		}
	} else if !errors.Is(err, want) {
		t.Fatalf("got %v want %v", err, want)
	}
}

type failingChain struct {
	calls   atomic.Int32
	started chan struct{}
}

func (f *failingChain) GetServiceURI(ctx context.Context, _ types.EthAddress) (string, error) {
	f.calls.Add(1)
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	<-ctx.Done()
	return "", ctx.Err()
}

type failingFetcher struct{ calls atomic.Int32 }

func (f *failingFetcher) Fetch(context.Context, string) ([]byte, error) {
	f.calls.Add(1)
	return nil, types.ErrManifestUnavailable
}

type countingHealth struct {
	calls atomic.Int32
	snap  *types.RouteHealthSnapshot
}

func (f *countingHealth) Fetch(context.Context, string) (*types.RouteHealthSnapshot, error) {
	f.calls.Add(1)
	return f.snap, nil
}

func TestSnapshotSelectionNeverCallsProviders(t *testing.T) {
	f := routeFixture(t)
	expectSelection(t, f.svc, types.ErrRegistryUnavailable)
	warmRoute(t, f)
	c := &failingChain{started: make(chan struct{}, 1)}
	m := &failingFetcher{}
	h := &countingHealth{}
	f.svc.chain = c
	f.svc.fetcher = m
	f.svc.liveHealth = h
	// The configured RPC provider is stalled on an unrelated orchestrator.
	other := types.EthAddress("0x1111111111111111111111111111111111111111")
	f.svc.Discover([]types.EthAddress{f.addr, other})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = f.svc.RefreshAddress(ctx, other) }()
	<-c.started
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				expectSelection(t, f.svc, nil)
			}
		}()
	}
	wg.Wait()
	cancel()
	<-done
	if c.calls.Load() != 1 || m.calls.Load() != 0 || h.calls.Load() != 0 {
		t.Fatalf("selection performed I/O chain=%d manifest=%d health=%d", c.calls.Load(), m.calls.Load(), h.calls.Load())
	}
}
func TestSnapshotExpiryAndNegativeSemantics(t *testing.T) {
	for _, what := range []string{"health", "metadata", "manifest", "keys"} {
		t.Run(what, func(t *testing.T) {
			f := routeFixture(t)
			warmRoute(t, f)
			switch what {
			case "health":
				f.clk.Advance(refreshTimeout)
			case "metadata":
				f.clk.Advance(f.svc.manifest)
				f.health.snapshots[testBroker].Capabilities[0].StaleAfter = f.clk.Now().Add(time.Minute)
				f.svc.RefreshHealth(context.Background(), testBroker)
			case "manifest":
				f.svc.routes.mu.Lock()
				st := f.svc.routes.addresses[f.addr]
				st.until = f.clk.Now()
				f.svc.routes.addresses[f.addr] = st
				f.svc.publishLocked()
				f.svc.routes.mu.Unlock()
			case "keys":
				f.svc.routes.mu.Lock()
				st := f.svc.routes.addresses[f.addr]
				st.nodes[0].SettlementKeys = []types.SettlementKey{{NotBefore: f.clk.Now().Add(-time.Hour), ExpiresAt: f.clk.Now()}}
				f.svc.publishLocked()
				f.svc.routes.mu.Unlock()
			}
			expectSelection(t, f.svc, types.ErrRegistryUnavailable)
		})
	}
	f := routeFixture(t)
	warmRoute(t, f)
	if _, err := f.svc.SelectNodes(context.Background(), selection.Filter{Capability: "absent", Offering: "absent"}); !errors.Is(err, types.ErrNotFound) {
		t.Fatal(err)
	}
	f.health.snapshots[testBroker].Capabilities[0].Status = "degraded"
	f.svc.RefreshHealth(context.Background(), testBroker)
	expectSelection(t, f.svc, types.ErrNotFound)
	f.health.snapshots[testBroker].Capabilities[0].StaleAfter = time.Time{}
	f.svc.RefreshHealth(context.Background(), testBroker)
	expectSelection(t, f.svc, types.ErrRegistryUnavailable)
}
func TestRefreshFailureDoesNotRenewOrConcealInvalidData(t *testing.T) {
	f := routeFixture(t)
	warmRoute(t, f)
	f.svc.fetcher = &failingFetcher{}
	if err := f.svc.RefreshAddress(context.Background(), f.addr); err == nil {
		t.Fatal("expected failure")
	}
	expectSelection(t, f.svc, nil) // Previously verified data is still within its hard bounds.
	f.svc.fetcher = f.fetcher
	f.fetcher.Bodies[f.uri] = []byte(`{"invalid":"signature"}`)
	if err := f.svc.RefreshAddress(context.Background(), f.addr); err == nil {
		t.Fatal("expected validation failure")
	}
	expectSelection(t, f.svc, types.ErrRegistryUnavailable)
	// A later explicit resolve cannot resurrect the cached publication after validation failure.
	_, _ = f.svc.ResolveByAddress(context.Background(), Request{Address: f.addr})
	expectSelection(t, f.svc, types.ErrRegistryUnavailable)
}
func TestChangedChainPointerRevokesOldRoute(t *testing.T) {
	f := routeFixture(t)
	warmRoute(t, f)
	f.chain.PreLoad(f.addr, "https://replacement.example.com/manifest.json")
	if err := f.svc.RefreshAddress(context.Background(), f.addr); err == nil {
		t.Fatal("expected fetch failure")
	}
	expectSelection(t, f.svc, types.ErrRegistryUnavailable)
}
func TestRestartDoesNotTrustPersistedHealth(t *testing.T) {
	f := routeFixture(t)
	warmRoute(t, f)
	restarted := New(Config{Chain: f.chain, Fetcher: f.fetcher, Cache: f.cache, Clock: f.clk, LiveHealth: f.health})
	restarted.Discover([]types.EthAddress{f.addr})
	expectSelection(t, restarted, types.ErrRegistryUnavailable)
	if err := restarted.RefreshAddress(context.Background(), f.addr); err != nil {
		t.Fatal(err)
	}
	expectSelection(t, restarted, types.ErrRegistryUnavailable)
	restarted.RefreshHealth(context.Background(), testBroker)
	expectSelection(t, restarted, nil)
}
func TestOverlayGenerationAndPoolRemoval(t *testing.T) {
	f := routeFixture(t)
	warmRoute(t, f)
	var ov atomic.Pointer[config.Overlay]
	ov.Store(f.overlay)
	f.svc.overlay = ov.Load
	ov.Store(config.EmptyOverlay())
	expectSelection(t, f.svc, types.ErrRegistryUnavailable)
	warmRoute(t, f)
	expectSelection(t, f.svc, nil)
	f.svc.Discover(nil)
	expectSelection(t, f.svc, types.ErrNotFound)
}

type blockingFetcher struct {
	body    []byte
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (f *blockingFetcher) Fetch(ctx context.Context, _ string) ([]byte, error) {
	f.calls.Add(1)
	select {
	case f.entered <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-f.release:
		return f.body, nil
	}
}
func TestRefreshDeduplicationAndGenerationDiscard(t *testing.T) {
	f := routeFixture(t)
	b := &blockingFetcher{body: f.fetcher.Bodies[f.uri], entered: make(chan struct{}, 1), release: make(chan struct{})}
	f.svc.fetcher = b
	var ov atomic.Pointer[config.Overlay]
	ov.Store(f.overlay)
	f.svc.overlay = ov.Load
	done := make(chan error, 1)
	go func() { done <- f.svc.RefreshAddress(context.Background(), f.addr) }()
	<-b.entered
	ov.Store(config.EmptyOverlay())
	close(b.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	expectSelection(t, f.svc, types.ErrRegistryUnavailable)
	// Concurrent refreshes wait on the address lock and reuse a completed attempt.
	b = &blockingFetcher{body: b.body, entered: make(chan struct{}, 1), release: make(chan struct{})}
	f.svc.fetcher = b
	go func() { done <- f.svc.RefreshAddress(context.Background(), f.addr) }()
	<-b.entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.svc.RefreshAddress(ctx, f.addr); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(b.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if b.calls.Load() != 1 {
		t.Fatal("duplicate refresh")
	}
}
func TestBackgroundWorkerBoundsAndShutdown(t *testing.T) {
	c := &failingChain{started: make(chan struct{}, 100)}
	s := New(Config{Chain: c, Cache: manifestcache.New(store.NewMemory()), Clock: clock.System{}})
	addresses := make([]types.EthAddress, 20)
	for i := range addresses {
		addresses[i] = types.EthAddress(fmt.Sprintf("0x%040x", i+1))
	}
	s.Discover(addresses)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.RunRefresh(ctx) }()
	for i := 0; i < refreshWorkers; i++ {
		select {
		case <-c.started:
		case <-time.After(time.Second):
			t.Fatal("workers did not start")
		}
	}
	if c.calls.Load() != refreshWorkers {
		t.Fatalf("calls=%d", c.calls.Load())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("workers did not stop")
	}
}

// Representative in-process read workload: 1,000 routes, 100 tuples, concurrent
// callers; provider fields are nil so any accidental outbound work would fail.
func BenchmarkSnapshotSelection(b *testing.B) {
	s := New(Config{Clock: clock.System{}})
	now := time.Now()
	snap := &routeSnapshot{overlay: s.overlay(), complete: true, coverageUntil: now.Add(time.Hour), index: map[tupleKey][]indexedRoute{}}
	for i := 0; i < 1000; i++ {
		c := fmt.Sprintf("cap:%d", i%100)
		n := types.ResolvedNode{ID: fmt.Sprint(i), Enabled: true, Weight: 100, Capabilities: []types.Capability{{Name: c, Offerings: []types.Offering{{ID: "default"}}}}}
		key := tuple(c, "default")
		snap.index[key] = append(snap.index[key], indexedRoute{node: n, until: now.Add(time.Hour), healthUntil: now.Add(time.Hour), healthKnown: true, ready: true})
	}
	s.routes.snapshot.Store(snap)
	var mu sync.Mutex
	var samples []int64
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		local := make([]int64, 0, 1024)
		for pb.Next() {
			start := time.Now()
			_, err := s.SelectNodes(context.Background(), selection.Filter{Capability: "cap:1", Offering: "default"})
			if err != nil {
				b.Fatal(err)
			}
			if len(local) < 1024 {
				local = append(local, time.Since(start).Nanoseconds())
			}
		}
		mu.Lock()
		samples = append(samples, local...)
		mu.Unlock()
	})
	b.StopTimer()
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	if len(samples) > 0 {
		b.ReportMetric(float64(samples[(len(samples)-1)*99/100])/1e6, "sample-p99-ms")
	}
}

func TestBackgroundWarmsWithoutSelectionIO(t *testing.T) {
	f := routeFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); f.svc.RunRefresh(ctx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		nodes, err := f.svc.SelectNodes(context.Background(), testFilter)
		if err == nil && len(nodes) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("background warming failed: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
}
func TestNegativeChainStateAndResultIsolation(t *testing.T) {
	f := routeFixture(t)
	warmRoute(t, f)
	nodes, err := f.svc.SelectNodes(context.Background(), testFilter)
	if err != nil {
		t.Fatal(err)
	}
	nodes[0].Capabilities[0].Offerings[0].ID = "mutated"
	expectSelection(t, f.svc, nil)
	f.svc.Discover(nil)
	expectSelection(t, f.svc, types.ErrNotFound)
	// A removed candidate must not be reintroduced by a late refresh completion.
	if err := f.svc.RefreshAddress(context.Background(), f.addr); err != nil {
		t.Fatal(err)
	}
	expectSelection(t, f.svc, types.ErrNotFound)
	missing := types.EthAddress("0x1111111111111111111111111111111111111111")
	f.svc.Discover([]types.EthAddress{missing})
	if err := f.svc.RefreshAddress(context.Background(), missing); !errors.Is(err, types.ErrNotFound) {
		t.Fatal(err)
	}
	expectSelection(t, f.svc, types.ErrNotFound)
	f.svc.DiscoveryFailed()
	expectSelection(t, f.svc, types.ErrRegistryUnavailable)
}

func TestSnapshotRejectsClockBeforeSignedWindow(t *testing.T) {
	f := routeFixture(t)
	warmRoute(t, f)
	f.clk.Advance(-time.Second)
	expectSelection(t, f.svc, types.ErrRegistryUnavailable)
	if _, err := f.svc.SelectNodes(context.Background(), selection.Filter{Capability: "absent", Offering: "default"}); !errors.Is(err, types.ErrRegistryUnavailable) {
		t.Fatal(err)
	}
}

type atomicManifestFetcher struct{ body atomic.Value }

func (f *atomicManifestFetcher) Fetch(context.Context, string) ([]byte, error) {
	return f.body.Load().([]byte), nil
}
func TestConcurrentPublicationSnapshotsStayConsistent(t *testing.T) {
	f := routeFixture(t)
	warmRoute(t, f)
	fetch := &atomicManifestFetcher{}
	original := f.fetcher.Bodies[f.uri]
	fetch.body.Store(original)
	f.svc.fetcher = fetch
	var env types.CoordinatorSignedManifest
	if err := json.Unmarshal(original, &env); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 1000; j++ {
				nodes, err := f.svc.SelectNodes(context.Background(), testFilter)
				if err != nil {
					t.Error(err)
					return
				}
				n := nodes[0]
				want := fmt.Sprint(n.PublicationSeq)
				if n.PublicationSeq == 1 {
					want = "7"
				}
				if got := n.Capabilities[0].Offerings[0].PricePerWorkUnitWei; got != want {
					t.Errorf("mixed publication: seq=%d price=%s", n.PublicationSeq, got)
					return
				}
			}
		}()
	}
	for seq := uint64(2); seq <= 20; seq++ {
		env.Manifest.PublicationSeq = seq
		env.Manifest.Capabilities[0].PricePerUnitWei = fmt.Sprint(seq)
		canonical, err := types.CoordinatorCanonicalBytes(env.Manifest)
		if err != nil {
			t.Fatal(err)
		}
		signature, err := f.signer.SignCanonical(canonical)
		if err != nil {
			t.Fatal(err)
		}
		env.Signature.Value = "0x" + hex(signature)
		body, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		fetch.body.Store(body)
		if err := f.svc.RefreshAddress(context.Background(), f.addr); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
	// The persistent replay watermark still rejects an older signed publication.
	fetch.body.Store(original)
	if err := f.svc.RefreshAddress(context.Background(), f.addr); err == nil {
		t.Fatal("rollback accepted")
	}
	expectSelection(t, f.svc, types.ErrRegistryUnavailable)
}
