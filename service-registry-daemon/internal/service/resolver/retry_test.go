package resolver

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/repo/manifestcache"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/selection"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

type outcomeFetcher struct {
	calls   atomic.Int32
	outcome map[string]error
	body    map[string][]byte
}

func (f *outcomeFetcher) Fetch(_ context.Context, u string) ([]byte, error) {
	f.calls.Add(1)
	if err, ok := f.outcome[u]; ok {
		return nil, err
	}
	return f.body[u], nil
}
func httpFailure(code int) error {
	return &types.FetchError{Cause: fmt.Errorf("%w: HTTP %d", types.ErrManifestUnavailable, code), HTTPStatus: code}
}

func TestClassifiedRetrySchedulesAndNoOrdinaryIO(t *testing.T) {
	for _, kind := range []string{"incompatible", "compatible", "unknown", "rejected"} {
		t.Run(kind, func(t *testing.T) {
			f := routeFixture(t)
			f.svc.retry.Jitter = 0
			if kind == "compatible" {
				warmRoute(t, f)
			}
			fetch := &outcomeFetcher{outcome: map[string]error{f.uri: types.ErrManifestUnavailable}}
			switch kind {
			case "incompatible":
				fetch.outcome[f.uri] = httpFailure(404)
			case "rejected":
				fetch.outcome[f.uri] = types.ErrSignatureMismatch
			}
			f.svc.fetcher = fetch
			var want config.Durations
			switch kind {
			case "incompatible":
				want = f.svc.retry.Incompatible
			case "compatible":
				want = f.svc.retry.Compatible
			case "unknown":
				want = f.svc.retry.Unknown
			case "rejected":
				want = f.svc.retry.Rejected
			}
			for i := 0; i < len(want)+2; i++ {
				if err := f.svc.refreshAddress(context.Background(), f.addr, i == 0); err == nil {
					t.Fatal("failure accepted")
				}
				st := f.svc.state(f.addr).status
				step := i
				if step >= len(want) {
					step = len(want) - 1
				}
				if got := st.NextRetryAt.Sub(f.clk.Now()); got != want[step] {
					t.Fatalf("step=%d delay=%v want=%v", i, got, want[step])
				}
				if st.RetryClass != kind || st.ConsecutiveFailures != uint32(i+1) {
					t.Fatal(st)
				}
				before := fetch.calls.Load()
				for n := 0; n < 3; n++ {
					res, err := f.svc.ResolveByAddress(context.Background(), Request{Address: f.addr})
					if err != nil && !errors.Is(err, types.ErrResolutionDeferred) {
						t.Fatal(err)
					}
					if res != nil && res.DiscoveryStatus.ConsecutiveFailures != st.ConsecutiveFailures {
						t.Fatal("cache reset streak")
					}
				}
				f.svc.Discover([]types.EthAddress{f.addr})
				if fetch.calls.Load() != before || f.svc.state(f.addr).status != st {
					t.Fatal("ordinary read or rediscovery changed retry")
				}
				f.clk.Advance(want[step])
			}
			st := f.svc.state(f.addr).status
			if kind == "incompatible" && st.Compatibility != types.CompatibilityIncompatible {
				t.Fatal(st)
			}
			if kind == "compatible" && st.Compatibility != types.CompatibilityVerified {
				t.Fatal(st)
			}
			if kind == "unknown" && st.Compatibility != types.CompatibilityUnknown {
				t.Fatal(st)
			}
		})
	}
}
func TestCandidateEvidenceAndHTTPClassification(t *testing.T) {
	for _, tt := range []struct {
		name     string
		outcomes []error
		bodies   [][]byte
		reason   types.FailureReason
		compat   types.Compatibility
	}{
		{"all_missing", []error{httpFailure(404), httpFailure(410), httpFailure(404)}, nil, types.FailureMissing, types.CompatibilityIncompatible},
		{"missing_then_timeout", []error{httpFailure(404), types.ErrManifestUnavailable, httpFailure(404)}, nil, types.FailureTransport, types.CompatibilityUnknown},
		{"server_error", []error{httpFailure(503), httpFailure(404), httpFailure(404)}, nil, types.FailureHTTP, types.CompatibilityUnknown},
		{"unsupported", []error{nil, httpFailure(404), httpFailure(404)}, [][]byte{[]byte(`{"schema_version":"3.0.1"}`)}, types.FailureUnsupported, types.CompatibilityIncompatible},
		{"unsupported_then_timeout", []error{nil, types.ErrManifestUnavailable, httpFailure(404)}, [][]byte{[]byte(`{"schema_version":"3.0.1"}`)}, types.FailureTransport, types.CompatibilityUnknown},
		{"malformed", []error{nil, httpFailure(404), httpFailure(404)}, [][]byte{[]byte(`not json`)}, types.FailureInvalid, types.CompatibilityUnknown},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			u := "https://orch.example.com"
			f.chain.PreLoad(f.addr, u)
			fetch := &outcomeFetcher{outcome: map[string]error{}, body: map[string][]byte{}}
			for i, url := range manifestFetchCandidates(u) {
				if tt.outcomes[i] != nil {
					fetch.outcome[url] = tt.outcomes[i]
				} else if i < len(tt.bodies) {
					fetch.body[url] = tt.bodies[i]
				}
			}
			f.svc.fetcher = fetch
			if err := f.svc.RefreshAddress(context.Background(), f.addr); err == nil {
				t.Fatal("unexpected verification")
			}
			got := f.svc.state(f.addr).status
			if got.FailureReason != tt.reason || got.Compatibility != tt.compat {
				t.Fatalf("%+v", got)
			}
		})
	}
}
func TestRestartPreservesCooldownAndRevocation(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprint(invalid), func(t *testing.T) {
			f := routeFixture(t)
			path := filepath.Join(t.TempDir(), "registry.db")
			db, err := store.OpenBolt(path)
			if err != nil {
				t.Fatal(err)
			}
			f.cache = manifestcache.New(db)
			f.svc.cache = f.cache
			warmRoute(t, f)
			fetch := &outcomeFetcher{outcome: map[string]error{f.uri: types.ErrManifestUnavailable}}
			if invalid {
				fetch.outcome[f.uri] = types.ErrSignatureMismatch
			}
			f.svc.fetcher = fetch
			if err := f.svc.RefreshAddress(context.Background(), f.addr); err == nil {
				t.Fatal("expected failure")
			}
			before := f.svc.state(f.addr).status
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = store.OpenBolt(path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			cache := manifestcache.New(db)
			restarted := New(Config{Chain: f.chain, Fetcher: fetch, Cache: cache, Clock: f.clk, LiveHealth: f.health})
			restarted.Discover([]types.EthAddress{f.addr})
			calls := fetch.calls.Load()
			res, err := restarted.ResolveByAddress(context.Background(), Request{Address: f.addr})
			if invalid && (!errors.Is(err, types.ErrResolutionDeferred) || res != nil) {
				t.Fatalf("revoked cache resurrected %v %v", res, err)
			}
			if !invalid && (err != nil || res == nil) {
				t.Fatalf("valid cache not restored %v", err)
			}
			if got := restarted.state(f.addr).status; got != before {
				t.Fatalf("state changed across restart: %+v %+v", got, before)
			}
			if fetch.calls.Load() != calls {
				t.Fatal("restart bypassed backoff")
			}
			expectSelection(t, restarted, types.ErrRegistryUnavailable) // health must warm anew
		})
	}
}
func TestNeverSuccessfulCandidatePersistsAndForceRecovers(t *testing.T) {
	f := routeFixture(t)
	good := f.fetcher
	fetch := &outcomeFetcher{outcome: map[string]error{f.uri: httpFailure(404)}}
	f.svc.fetcher = fetch
	if err := f.svc.RefreshAddress(context.Background(), f.addr); err == nil {
		t.Fatal("expected failure")
	}
	restarted := New(Config{Chain: f.chain, Fetcher: good, Cache: f.cache, Clock: f.clk, LiveHealth: f.health})
	addrs, err := restarted.CandidateAddresses()
	if err != nil || len(addrs) != 1 {
		t.Fatalf("candidate lost %v %v", addrs, err)
	}
	if _, err := restarted.ResolveByAddress(context.Background(), Request{Address: f.addr}); !errors.Is(err, types.ErrResolutionDeferred) {
		t.Fatal(err)
	}
	if _, err := restarted.ResolveByAddress(context.Background(), Request{Address: f.addr, ForceRefresh: true}); err != nil {
		t.Fatal(err)
	}
	st := restarted.state(f.addr).status
	if st.ConsecutiveFailures != 0 || st.FailureReason != types.FailureNone || st.Compatibility != types.CompatibilityVerified || st.Invalidated {
		t.Fatal(st)
	}
	// A cache read does not renew verification or scheduling timestamps.
	f.clk.Advance(time.Second)
	if _, err := restarted.ResolveByAddress(context.Background(), Request{Address: f.addr}); err != nil {
		t.Fatal(err)
	}
	if restarted.state(f.addr).status != st {
		t.Fatal("cache read changed verification evidence")
	}
}
func TestConcurrentForcedAndOrdinaryCallsShareAttempt(t *testing.T) {
	f := routeFixture(t)
	b := &blockingFetcher{body: f.fetcher.Bodies[f.uri], entered: make(chan struct{}, 1), release: make(chan struct{})}
	f.svc.fetcher = b
	const count = 16
	var wg sync.WaitGroup
	leaderCtx, cancel := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)
	go func() { leaderDone <- f.svc.RefreshAddress(leaderCtx, f.addr) }()
	<-b.entered
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(force bool) {
			defer wg.Done()
			_, err := f.svc.ResolveByAddress(context.Background(), Request{Address: f.addr, ForceRefresh: force})
			if err != nil {
				t.Error(err)
			}
		}(i%2 == 0)
	}
	deadline := time.Now().Add(time.Second)
	for {
		f.svc.routes.mu.Lock()
		joined := f.svc.routes.flights[f.addr].waiters
		f.svc.routes.mu.Unlock()
		if joined == count+1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("waiters never coalesced")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-leaderDone; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(b.release)
	wg.Wait()
	if b.calls.Load() != 1 {
		t.Fatalf("fetch calls %d", b.calls.Load())
	}
	if f.svc.state(f.addr).status.ConsecutiveFailures != 0 {
		t.Fatal("cancellation poisoned state")
	}
}
func TestSourcePollingBypassesOnlyActualSourceChanges(t *testing.T) {
	f := routeFixture(t)
	fetch := &outcomeFetcher{outcome: map[string]error{f.uri: httpFailure(404)}}
	f.svc.fetcher = fetch
	if err := f.svc.RefreshAddress(context.Background(), f.addr); err == nil {
		t.Fatal("expected failure")
	}
	before := f.svc.state(f.addr).status.NextRetryAt
	f.svc.pollSource(context.Background(), f.addr)
	if f.svc.state(f.addr).status.NextRetryAt != before {
		t.Fatal("unchanged source reset cooldown")
	}
	next := "https://replacement.example.com/.well-known/livepeer-registry.json"
	f.chain.PreLoad(f.addr, next)
	f.svc.pollSource(context.Background(), f.addr)
	st := f.svc.state(f.addr)
	if !st.next.IsZero() || st.status.Compatibility != types.CompatibilityUnknown || !st.status.Invalidated || st.status.SourceURI != next {
		t.Fatal(st)
	}
}
func TestCatalogSnapshotOnlyCoverageAndExpiry(t *testing.T) {
	f := routeFixture(t)
	cold, err := f.svc.ListOfferings(context.Background(), selection.Filter{}, false)
	if err != nil || cold.Completeness != "partial" || cold.Known != 1 {
		t.Fatalf("cold: %+v %v", cold, err)
	}
	warmRoute(t, f)
	c := &failingChain{started: make(chan struct{}, 1)}
	f.svc.chain = c
	fetch := &failingFetcher{}
	f.svc.fetcher = fetch
	other := types.EthAddress("0x1111111111111111111111111111111111111111")
	f.svc.Discover([]types.EthAddress{f.addr, other})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- f.svc.RefreshAddress(ctx, other) }()
	<-c.started
	defer func() { cancel(); <-done }()
	for i := 0; i < 100; i++ {
		out, err := f.svc.ListOfferings(context.Background(), selection.Filter{}, false)
		if err != nil || len(out.Entries) != 1 || !out.Entries[0].Selectable || out.Completeness != "partial" || out.Known != 2 || out.Verified != 1 {
			t.Fatalf("catalog %+v %v", out, err)
		}
	}
	filtered, _ := f.svc.ListOfferings(context.Background(), selection.Filter{Capability: "absent"}, false)
	if len(filtered.Entries) != 0 || filtered.Known != 2 || filtered.Completeness != "partial" {
		t.Fatal(filtered)
	}
	if c.calls.Load() != 1 || fetch.calls.Load() != 0 {
		t.Fatal("catalog triggered providers")
	}
	// Stop the worker before mutating the test clock.
	cancel()
	<-done
	done <- nil
	f.clk.Advance(f.svc.manifest)
	out, _ := f.svc.ListOfferings(context.Background(), selection.Filter{}, false)
	if len(out.Entries) != 0 || out.Expired != 1 {
		t.Fatal(out)
	}
	out, _ = f.svc.ListOfferings(context.Background(), selection.Filter{}, true)
	if len(out.Entries) != 1 || out.Entries[0].Selectable || !out.Entries[0].Expired {
		t.Fatal(out)
	}
}
func TestCatalogConclusiveEmptyEvidenceExpiresBeforeDailyRetry(t *testing.T) {
	f := routeFixture(t)
	f.svc.fetcher = &outcomeFetcher{outcome: map[string]error{f.uri: httpFailure(404)}}
	if err := f.svc.RefreshAddress(context.Background(), f.addr); err == nil {
		t.Fatal("expected missing")
	}
	out, _ := f.svc.ListOfferings(context.Background(), selection.Filter{}, false)
	if out.Completeness != "complete" || out.Incompatible != 1 || len(out.Entries) != 0 {
		t.Fatal(out)
	}
	f.clk.Advance(f.svc.manifest)
	out, _ = f.svc.ListOfferings(context.Background(), selection.Filter{}, false)
	if out.Completeness != "partial" {
		t.Fatal("negative evidence lifetime extended")
	}
}

func TestJitterAndRestartSchedulerRespectPersistedDeadline(t *testing.T) {
	f := routeFixture(t)
	fetch := &outcomeFetcher{outcome: map[string]error{f.uri: httpFailure(404)}}
	f.svc.fetcher = fetch
	if err := f.svc.RefreshAddress(context.Background(), f.addr); err == nil {
		t.Fatal("expected missing")
	}
	st := f.svc.state(f.addr).status
	delay := st.NextRetryAt.Sub(st.LastAttemptAt)
	if delay < 4*time.Minute || delay > 5*time.Minute {
		t.Fatalf("jitter outside bounds: %v", delay)
	}
	restarted := New(Config{Chain: f.chain, Fetcher: fetch, Cache: f.cache, Clock: f.clk})
	restarted.Discover([]types.EthAddress{f.addr})
	calls := fetch.calls.Load()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); restarted.RunRefresh(ctx) }()
	deadline := time.Now().Add(time.Second)
	for !restarted.state(f.addr).loaded {
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("scheduler did not restore state")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if fetch.calls.Load() != calls || restarted.state(f.addr).status != st {
		t.Fatal("restart scheduled a cooldown attempt")
	}
}

// Failure to persist the completion must not revive an old accepted publication
// after restart. The durable pre-attempt guard is deliberately written first.
type completionFailureCache struct {
	manifestcache.Repo
	writes int
	failAt int
}

func (c *completionFailureCache) PutDiscovery(a types.EthAddress, st types.DiscoveryStatus) error {
	c.writes++
	if c.writes == c.failAt {
		return errors.New("disk unavailable")
	}
	return c.Repo.PutDiscovery(a, st)
}
func TestCompletionPersistenceFailureCannotResurrectCache(t *testing.T) {
	f := routeFixture(t)
	warmRoute(t, f)
	fail := &completionFailureCache{Repo: f.cache, failAt: 3} // guard, observed source guard, completion
	f.svc.cache = fail
	f.svc.fetcher = &outcomeFetcher{outcome: map[string]error{f.uri: types.ErrSignatureMismatch}}
	if err := f.svc.RefreshAddress(context.Background(), f.addr); err == nil {
		t.Fatal("persistence failure swallowed")
	}
	expectSelection(t, f.svc, types.ErrRegistryUnavailable)
	restarted := New(Config{Chain: f.chain, Fetcher: f.fetcher, Cache: f.cache, Clock: f.clk, LiveHealth: f.health})
	if err := restarted.ensureAddress(context.Background(), f.addr); err != nil {
		t.Fatal(err)
	}
	if restarted.state(f.addr).entry != nil || !restarted.state(f.addr).status.Invalidated {
		t.Fatal("revoked durable data restored")
	}
}

func TestIncompatibleEvidenceSurvivesTransportWithoutRenewal(t *testing.T) {
	f := routeFixture(t)
	f.svc.retry.Jitter = 0
	f.svc.fetcher = &outcomeFetcher{outcome: map[string]error{f.uri: httpFailure(404)}}
	if err := f.svc.RefreshAddress(context.Background(), f.addr); err == nil {
		t.Fatal("expected missing")
	}
	before := f.svc.state(f.addr).status
	f.clk.Advance(time.Second)
	f.svc.fetcher = &failingFetcher{}
	if err := f.svc.RefreshAddress(context.Background(), f.addr); err == nil {
		t.Fatal("expected unavailable")
	}
	after := f.svc.state(f.addr).status
	if after.Compatibility != types.CompatibilityIncompatible || after.CompatibilityValidUntil != before.CompatibilityValidUntil || after.NextRetryAt.Sub(f.clk.Now()) != 30*time.Minute {
		t.Fatal(after)
	}
}
