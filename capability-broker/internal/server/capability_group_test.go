package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/poolsnapshot"
)

func TestSelectBackendSkipsDrainingAndUnreachableCandidates(t *testing.T) {
	cfg := &config.Config{
		Capabilities: []config.Capability{
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Backend:    config.Backend{ID: "backend-ready", URL: "http://ready"},
				Health:     config.Health{InitialStatus: "ready"},
			},
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Backend:    config.Backend{ID: "backend-draining", URL: "http://draining"},
				Health:     config.Health{InitialStatus: "draining", Drain: config.HealthDrain{Enabled: true}},
			},
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Backend:    config.Backend{ID: "backend-dead", URL: "http://dead"},
				Health:     config.Health{InitialStatus: "unreachable"},
			},
		},
	}
	s := &Server{cfg: cfg, health: health.New(cfg)}

	group, ok := s.groupFor("openai:chat-completions", "shared")
	if !ok {
		t.Fatal("groupFor() = not found, want found")
	}
	selected, err := s.selectBackend(group)
	if err != nil {
		t.Fatalf("selectBackend() error = %v", err)
	}
	if got := selected.Backend.ID; got != "backend-ready" {
		t.Fatalf("selected backend = %q, want backend-ready", got)
	}
}

func TestBackendSelectionWeightPrefersFreshSuccessfulReadyBackend(t *testing.T) {
	now := nowUTC()
	ready := backendSelectionWeight(health.Snapshot{
		Status:               health.StatusReady,
		ProbedAt:             now.Add(-2 * time.Second),
		StaleAfter:           now.Add(8 * time.Second),
		ConsecutiveSuccesses: 4,
	})
	degraded := backendSelectionWeight(health.Snapshot{
		Status:               health.StatusDegraded,
		ProbedAt:             now.Add(-2 * time.Second),
		StaleAfter:           now.Add(8 * time.Second),
		ConsecutiveSuccesses: 2,
	})
	if ready <= degraded {
		t.Fatalf("ready weight = %d, degraded weight = %d; want ready > degraded", ready, degraded)
	}
}

func TestBackendSelectionWeightDropsNearStaleAndFailureHeavyBackend(t *testing.T) {
	now := nowUTC()
	stable := backendSelectionWeight(health.Snapshot{
		Status:               health.StatusReady,
		ProbedAt:             now.Add(-1 * time.Second),
		StaleAfter:           now.Add(11 * time.Second),
		ConsecutiveSuccesses: 3,
	})
	nearStale := backendSelectionWeight(health.Snapshot{
		Status:               health.StatusReady,
		ProbedAt:             now.Add(-9 * time.Second),
		StaleAfter:           now.Add(1 * time.Second),
		ConsecutiveSuccesses: 3,
	})
	if nearStale >= stable {
		t.Fatalf("nearStale weight = %d, stable weight = %d; want nearStale < stable", nearStale, stable)
	}
	failureHeavy := backendSelectionWeight(health.Snapshot{
		Status:              health.StatusDegraded,
		ProbedAt:            now.Add(-1 * time.Second),
		StaleAfter:          now.Add(9 * time.Second),
		ConsecutiveFailures: 5,
	})
	if failureHeavy != 0 {
		t.Fatalf("failureHeavy weight = %d, want 0", failureHeavy)
	}
}

func TestSelectBackendUsesWeightedPickFunction(t *testing.T) {
	cfg := &config.Config{
		Capabilities: []config.Capability{
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Backend:    config.Backend{ID: "backend-a", URL: "http://a"},
				Health:     config.Health{InitialStatus: "ready"},
			},
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Backend:    config.Backend{ID: "backend-b", URL: "http://b"},
				Health:     config.Health{InitialStatus: "ready"},
			},
		},
	}
	s := &Server{cfg: cfg, health: health.New(cfg), randIntn: func(n int) int { return n - 1 }}
	group, ok := s.groupFor("openai:chat-completions", "shared")
	if !ok {
		t.Fatal("groupFor() = not found, want found")
	}
	selected, err := s.selectBackend(group)
	if err != nil {
		t.Fatalf("selectBackend() error = %v", err)
	}
	if got := selected.Backend.ID; got != "backend-b" {
		t.Fatalf("selected backend = %q, want backend-b", got)
	}
}

func TestSelectBackendSkipsBackendAtMaxInFlight(t *testing.T) {
	cfg := &config.Config{
		Capabilities: []config.Capability{
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Backend:    config.Backend{ID: "backend-a", URL: "http://a", MaxInFlight: 1},
				Health:     config.Health{InitialStatus: "ready"},
			},
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Backend:    config.Backend{ID: "backend-b", URL: "http://b", MaxInFlight: 1},
				Health:     config.Health{InitialStatus: "ready"},
			},
		},
	}
	s := &Server{
		cfg:             cfg,
		health:          health.New(cfg),
		backendInFlight: map[string]int{"backend-a": 1},
	}

	group, ok := s.groupFor("openai:chat-completions", "shared")
	if !ok {
		t.Fatal("groupFor() = not found, want found")
	}
	selected, err := s.selectBackend(group)
	if err != nil {
		t.Fatalf("selectBackend() error = %v", err)
	}
	if got := selected.Backend.ID; got != "backend-b" {
		t.Fatalf("selected backend = %q, want backend-b", got)
	}
}

func TestReserveBackendHonorsMaxInFlightAndReleases(t *testing.T) {
	cap := &config.Capability{
		Backend: config.Backend{ID: "backend-a", MaxInFlight: 1},
	}
	s := &Server{backendInFlight: map[string]int{}}

	release, ok := s.reserveBackend(cap)
	if !ok {
		t.Fatal("first reserveBackend() = false, want true")
	}
	if _, ok := s.reserveBackend(cap); ok {
		t.Fatal("second reserveBackend() = true, want false while backend is full")
	}
	release()
	if _, ok := s.reserveBackend(cap); !ok {
		t.Fatal("reserveBackend() after release = false, want true")
	}
}

func TestSelectBackendRespectsPoolExcludedState(t *testing.T) {
	cfg := &config.Config{
		Capabilities: []config.Capability{
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Backend:    config.Backend{ID: "backend-a", URL: "http://a"},
				Health:     config.Health{InitialStatus: "ready"},
			},
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Backend:    config.Backend{ID: "backend-b", URL: "http://b"},
				Health:     config.Health{InitialStatus: "ready"},
			},
		},
		PoolSnapshot: config.PoolSnapshot{URL: "http://pool-controller:8080"},
	}
	s := &Server{cfg: cfg, health: health.New(cfg)}
	s.poolSnapshot = loadTestPoolSnapshot(t, fmt.Sprintf(`{
		"generated_at":%q,
		"entries":[
			{"backend_id":"backend-a","capability_id":"openai:chat-completions","offering_id":"shared","state":"excluded","effective_selection_score":0.5},
			{"backend_id":"backend-b","capability_id":"openai:chat-completions","offering_id":"shared","state":"eligible","effective_selection_score":0.5}
		]
	}`, time.Now().UTC().Format(time.RFC3339)))
	if status := s.poolSnapshot.StatusFor("backend-b", "openai:chat-completions", "shared"); !status.EntryFound || status.SnapshotStatus != "fresh" || status.EntryEffectiveSelectionScore != 0.5 {
		t.Fatalf("unexpected backend-b pool status: %+v", status)
	}

	group, ok := s.groupFor("openai:chat-completions", "shared")
	if !ok {
		t.Fatal("groupFor() = not found, want found")
	}
	selected, err := s.selectBackend(group)
	if err != nil {
		t.Fatalf("selectBackend() error = %v", err)
	}
	if got := selected.Backend.ID; got != "backend-b" {
		t.Fatalf("selected backend = %q, want backend-b", got)
	}
}

func TestSelectBackendAppliesPoolScoreWeight(t *testing.T) {
	cfg := &config.Config{
		Capabilities: []config.Capability{
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Backend:    config.Backend{ID: "backend-a", URL: "http://a"},
				Health:     config.Health{InitialStatus: "ready"},
			},
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Backend:    config.Backend{ID: "backend-b", URL: "http://b"},
				Health:     config.Health{InitialStatus: "ready"},
			},
		},
		PoolSnapshot: config.PoolSnapshot{URL: "http://pool-controller:8080"},
	}
	s := &Server{cfg: cfg, health: health.New(cfg), randIntn: func(n int) int { return 60 }}
	s.poolSnapshot = loadTestPoolSnapshot(t, fmt.Sprintf(`{
		"generated_at":%q,
		"entries":[
			{"backend_id":"backend-a","capability_id":"openai:chat-completions","offering_id":"shared","state":"eligible","effective_selection_score":0.1},
			{"backend_id":"backend-b","capability_id":"openai:chat-completions","offering_id":"shared","state":"eligible","effective_selection_score":0.9}
		]
	}`, time.Now().UTC().Format(time.RFC3339)))
	if status := s.poolSnapshot.StatusFor("backend-b", "openai:chat-completions", "shared"); !status.EntryFound {
		t.Fatalf("backend-b pool status not found: %+v", status)
	}

	group, ok := s.groupFor("openai:chat-completions", "shared")
	if !ok {
		t.Fatal("groupFor() = not found, want found")
	}
	selected, err := s.selectBackend(group)
	if err != nil {
		t.Fatalf("selectBackend() error = %v", err)
	}
	if got := selected.Backend.ID; got != "backend-b" {
		t.Fatalf("selected backend = %q, want backend-b", got)
	}
}

func TestSelectBackendEnforcesPoolMaxShareCap(t *testing.T) {
	cfg := &config.Config{
		Capabilities: []config.Capability{
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Backend:    config.Backend{ID: "backend-a", URL: "http://a"},
				Health:     config.Health{InitialStatus: "ready"},
			},
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Backend:    config.Backend{ID: "backend-b", URL: "http://b"},
				Health:     config.Health{InitialStatus: "ready"},
			},
		},
		PoolSnapshot: config.PoolSnapshot{URL: "http://pool-controller:8080"},
	}
	s := &Server{cfg: cfg, health: health.New(cfg), randIntn: func(n int) int { return 50 }}
	s.poolSnapshot = loadTestPoolSnapshot(t, fmt.Sprintf(`{
		"generated_at":%q,
		"entries":[
			{"backend_id":"backend-a","capability_id":"openai:chat-completions","offering_id":"shared","state":"eligible","effective_selection_score":1.0,"max_share_cap":0.20},
			{"backend_id":"backend-b","capability_id":"openai:chat-completions","offering_id":"shared","state":"eligible","effective_selection_score":1.0}
		]
	}`, time.Now().UTC().Format(time.RFC3339)))

	group, ok := s.groupFor("openai:chat-completions", "shared")
	if !ok {
		t.Fatal("groupFor() = not found, want found")
	}
	selected, err := s.selectBackend(group)
	if err != nil {
		t.Fatalf("selectBackend() error = %v", err)
	}
	if got := selected.Backend.ID; got != "backend-b" {
		t.Fatalf("selected backend with cap = %q, want backend-b", got)
	}
}

func TestSelectBackendRecordsExhaustedReasonWhenAllCandidatesBlocked(t *testing.T) {
	cfg := &config.Config{
		Capabilities: []config.Capability{
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Backend:    config.Backend{ID: "backend-a", URL: "http://a"},
				Health:     config.Health{InitialStatus: "ready"},
			},
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Backend:    config.Backend{ID: "backend-b", URL: "http://b"},
				Health:     config.Health{InitialStatus: "ready"},
			},
		},
		PoolSnapshot: config.PoolSnapshot{URL: "http://pool-controller:8080"},
	}
	s := &Server{cfg: cfg, health: health.New(cfg)}
	s.poolSnapshot = loadTestPoolSnapshot(t, fmt.Sprintf(`{
		"generated_at":%q,
		"entries":[
			{"backend_id":"backend-a","capability_id":"openai:chat-completions","offering_id":"shared","state":"excluded","routing_reason":"pool_cooldown","exclusion_reason":"pool_cooldown","effective_selection_score":0.5},
			{"backend_id":"backend-b","capability_id":"openai:chat-completions","offering_id":"shared","state":"excluded","routing_reason":"pool_cooldown","exclusion_reason":"pool_cooldown","effective_selection_score":0.5}
		]
	}`, time.Now().UTC().Format(time.RFC3339)))

	group, ok := s.groupFor("openai:chat-completions", "shared")
	if !ok {
		t.Fatal("groupFor() = not found, want found")
	}
	selected, err := s.selectBackend(group)
	if err == nil {
		t.Fatalf("selectBackend() error = nil, selected = %+v; want exhaustion error", selected)
	}
}

func loadTestPoolSnapshot(t *testing.T, body string) *poolsnapshot.Cache {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)

	cache, err := poolsnapshot.New(config.PoolSnapshot{
		URL:            ts.URL,
		TimeoutMS:      100,
		PollIntervalMS: 1000,
		StaleAfterMS:   15000,
		ExpireAfterMS:  60000,
	}, nil)
	if err != nil {
		t.Fatalf("poolsnapshot.New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		cache.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if status := cache.StatusFor("backend-a", "openai:chat-completions", "shared"); status.EntryFound || status.SnapshotStatus == "fresh" {
			return cache
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for pool snapshot cache to populate")
	return nil
}
