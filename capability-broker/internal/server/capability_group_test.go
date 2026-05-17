package server

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
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
