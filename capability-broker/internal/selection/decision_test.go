package selection

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/poolsnapshot"
)

func TestProbeWeightPrefersFreshReadyBackend(t *testing.T) {
	now := time.Now().UTC()
	ready := ProbeWeight(health.Snapshot{
		Status:               health.StatusReady,
		ProbedAt:             now.Add(-2 * time.Second),
		StaleAfter:           now.Add(8 * time.Second),
		ConsecutiveSuccesses: 4,
	})
	degraded := ProbeWeight(health.Snapshot{
		Status:               health.StatusDegraded,
		ProbedAt:             now.Add(-2 * time.Second),
		StaleAfter:           now.Add(8 * time.Second),
		ConsecutiveSuccesses: 2,
	})
	if ready <= degraded {
		t.Fatalf("ready weight = %d, degraded weight = %d; want ready > degraded", ready, degraded)
	}
}

func TestProbeWeightDiscountsNearStaleAndFailureHeavyBackend(t *testing.T) {
	now := time.Now().UTC()
	stable := ProbeWeight(health.Snapshot{
		Status:               health.StatusReady,
		ProbedAt:             now.Add(-1 * time.Second),
		StaleAfter:           now.Add(11 * time.Second),
		ConsecutiveSuccesses: 3,
	})
	nearStale := ProbeWeight(health.Snapshot{
		Status:               health.StatusReady,
		ProbedAt:             now.Add(-9 * time.Second),
		StaleAfter:           now.Add(1 * time.Second),
		ConsecutiveSuccesses: 3,
	})
	if nearStale >= stable {
		t.Fatalf("nearStale weight = %d, stable weight = %d; want nearStale < stable", nearStale, stable)
	}
	failureHeavy := ProbeWeight(health.Snapshot{
		Status:              health.StatusDegraded,
		ProbedAt:            now.Add(-1 * time.Second),
		StaleAfter:          now.Add(9 * time.Second),
		ConsecutiveFailures: 5,
	})
	if failureHeavy != 0 {
		t.Fatalf("failureHeavy weight = %d, want 0", failureHeavy)
	}
}

func TestDecisionForPoolExcludedBackend(t *testing.T) {
	decision := DecisionFor(readySnapshot(), &poolsnapshot.Status{
		Configured:     true,
		SnapshotStatus: "fresh",
		EntryFound:     true,
		EntryState:     "excluded",
	})
	if decision.Eligible {
		t.Fatalf("decision eligible = true, want false: %+v", decision)
	}
	if decision.Reason != "pool_excluded" {
		t.Fatalf("decision reason = %q, want pool_excluded", decision.Reason)
	}
}

func TestDecisionForUsesPoolRoutingReasonAndShareCap(t *testing.T) {
	decision := DecisionFor(readySnapshot(), &poolsnapshot.Status{
		Configured:                   true,
		SnapshotStatus:               "fresh",
		EntryFound:                   true,
		EntryState:                   "eligible",
		EntryRoutingReason:           "pool_warmup",
		EntryEffectiveSelectionScore: 0.5,
		EntryMaxShareCap:             0.4,
	})
	if !decision.Eligible {
		t.Fatalf("decision eligible = false, want true: %+v", decision)
	}
	if decision.Weight <= 0 {
		t.Fatalf("decision weight = %d, want positive", decision.Weight)
	}
	if decision.Reason != "pool_warmup" {
		t.Fatalf("decision reason = %q, want pool_warmup", decision.Reason)
	}
	if decision.MaxShareCap != 0.4 {
		t.Fatalf("decision maxShareCap = %v, want 0.4", decision.MaxShareCap)
	}
}

func TestDecisionForRejectsExpiredSnapshot(t *testing.T) {
	decision := DecisionFor(readySnapshot(), &poolsnapshot.Status{
		Configured:     true,
		SnapshotStatus: "expired",
	})
	if decision.Eligible {
		t.Fatalf("decision eligible = true, want false: %+v", decision)
	}
	if decision.Reason != "pool_snapshot_expired" {
		t.Fatalf("decision reason = %q, want pool_snapshot_expired", decision.Reason)
	}
}

func TestDecisionForUsesStaleWindowReasons(t *testing.T) {
	decision := DecisionFor(readySnapshot(), &poolsnapshot.Status{
		Configured:                            true,
		SnapshotStatus:                        "fresh",
		EntryFound:                            true,
		EntryState:                            "degraded",
		EntryEffectiveSelectionScore:          0.2,
		EntryRecentWindowAgeSeconds:           301,
		SnapshotRecentWindowStaleAfterSeconds: 300,
	})
	if !decision.Eligible {
		t.Fatalf("decision eligible = false, want true: %+v", decision)
	}
	if decision.Reason != "pool_degraded_stale_sample_window" {
		t.Fatalf("decision reason = %q, want pool_degraded_stale_sample_window", decision.Reason)
	}
}

func readySnapshot() health.Snapshot {
	now := time.Now().UTC()
	return health.Snapshot{
		Status:               health.StatusReady,
		ProbedAt:             now.Add(-1 * time.Second),
		StaleAfter:           now.Add(9 * time.Second),
		ConsecutiveSuccesses: 3,
	}
}
