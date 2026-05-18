package selection

import (
	"math"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/poolsnapshot"
)

type Decision struct {
	Eligible    bool
	Weight      int
	MaxShareCap float64
	Reason      string
}

func ProbeWeight(snap health.Snapshot) int {
	base := 0
	switch snap.Status {
	case health.StatusReady:
		base = 100
	case health.StatusDegraded:
		base = 25
	default:
		return 0
	}
	if !snap.StaleAfter.IsZero() && snap.StaleAfter.Before(time.Now().UTC()) {
		return 0
	}
	successBonus := minInt(snap.ConsecutiveSuccesses, 5) * 10
	failurePenalty := minInt(snap.ConsecutiveFailures, 5) * 15
	weight := base + successBonus - failurePenalty
	if weight <= 0 {
		return 0
	}
	if isNearStale(snap) {
		weight /= 2
	}
	if weight <= 0 {
		return 1
	}
	return weight
}

func ProbeReason(snap health.Snapshot) string {
	switch snap.Status {
	case health.StatusReady, health.StatusDegraded:
	default:
		return "status_not_selectable"
	}
	if !snap.StaleAfter.IsZero() && snap.StaleAfter.Before(time.Now().UTC()) {
		return "stale"
	}
	if ProbeWeight(snap) == 0 {
		return "failure_penalized"
	}
	if isNearStale(snap) {
		return "near_stale_discounted"
	}
	return "eligible"
}

func DecisionFor(snap health.Snapshot, pool *poolsnapshot.Status) Decision {
	baseWeight := ProbeWeight(snap)
	if baseWeight == 0 {
		return Decision{
			Eligible: false,
			Weight:   0,
			Reason:   ProbeReason(snap),
		}
	}
	if pool == nil || !pool.Configured {
		return Decision{
			Eligible: true,
			Weight:   baseWeight,
			Reason:   ProbeReason(snap),
		}
	}
	switch pool.SnapshotStatus {
	case "fresh", "stale":
	default:
		return Decision{
			Eligible: false,
			Weight:   0,
			Reason:   "pool_snapshot_" + pool.SnapshotStatus,
		}
	}
	if !pool.EntryFound {
		return Decision{
			Eligible: false,
			Weight:   0,
			Reason:   "pool_snapshot_entry_missing",
		}
	}
	switch pool.EntryState {
	case "quarantined":
		return Decision{
			Eligible: false,
			Weight:   0,
			Reason:   firstReason(pool.EntryRoutingReason, pool.EntryExclusionReason, "pool_quarantined"),
		}
	case "excluded":
		return Decision{
			Eligible: false,
			Weight:   0,
			Reason:   firstReason(pool.EntryRoutingReason, pool.EntryExclusionReason, "pool_excluded"),
		}
	case "", "eligible", "degraded":
	default:
		return Decision{
			Eligible: false,
			Weight:   0,
			Reason:   "pool_state_not_selectable",
		}
	}
	if pool.EntryEffectiveSelectionScore <= 0 {
		return Decision{
			Eligible: false,
			Weight:   0,
			Reason:   "pool_score_zero",
		}
	}
	weighted := int(math.Round(float64(baseWeight) * pool.EntryEffectiveSelectionScore))
	if weighted <= 0 {
		weighted = 1
	}
	return Decision{
		Eligible:    true,
		Weight:      weighted,
		MaxShareCap: clampShareCap(pool.EntryMaxShareCap),
		Reason:      poolSelectionReason(snap, *pool),
	}
}

func poolSelectionReason(snap health.Snapshot, status poolsnapshot.Status) string {
	if status.EntryRoutingReason != "" {
		if status.EntryState == "eligible" || status.EntryState == "degraded" {
			return status.EntryRoutingReason
		}
	}
	if status.EntryState == "degraded" {
		switch {
		case RecentWindowStale(status):
			return "pool_degraded_stale_sample_window"
		case status.EntryEffectiveSelectionScore < 0.30:
			return "pool_degraded_low_score"
		default:
			return "pool_degraded"
		}
	}
	switch {
	case status.EntryAutomaticWarmup:
		return "pool_warmup"
	case status.SnapshotStatus == "stale":
		return "pool_snapshot_stale"
	case RecentWindowStale(status):
		return "pool_eligible_stale_sample_window"
	case isNearStale(snap):
		return "pool_eligible_near_stale_discounted"
	default:
		return "pool_eligible"
	}
}

func RecentWindowStale(status poolsnapshot.Status) bool {
	if status.EntryRecentWindowAgeSeconds <= 0 {
		return false
	}
	threshold := 5 * time.Minute
	if status.SnapshotRecentWindowStaleAfterSeconds > 0 {
		threshold = time.Duration(status.SnapshotRecentWindowStaleAfterSeconds * float64(time.Second))
	}
	return time.Duration(status.EntryRecentWindowAgeSeconds*float64(time.Second)) >= threshold
}

func firstReason(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func clampShareCap(value float64) float64 {
	switch {
	case value <= 0:
		return 0
	case value >= 1:
		return 1
	default:
		return value
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func isNearStale(snap health.Snapshot) bool {
	if snap.ProbedAt.IsZero() || snap.StaleAfter.IsZero() {
		return false
	}
	ttl := snap.StaleAfter.Sub(snap.ProbedAt)
	if ttl <= 0 {
		return false
	}
	remaining := snap.StaleAfter.Sub(time.Now().UTC())
	return remaining > 0 && remaining*4 < ttl
}
