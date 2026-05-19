// Package autodrain implements the policy-driven auto-drain rule for
// member backends. When the policy worker invokes RunOnce, the service
// inspects each persisted BackendSelectionState (the rolling-window
// view the broker already maintains for every member-backend +
// capability + offering triple) and computes a failure rate from
// RecentBackendFailureCount and RecentRoutableOutcomeCount. Any backend
// whose worst per-offering failure rate exceeds the threshold — and
// has at least Settings.MinSamples routable outcomes in that window —
// is transitioned from BackendStatusActive to BackendStatusDraining
// via statusservice. Disabling is not performed automatically; an
// operator must decide whether to keep the backend drained or to
// re-enable it.
package autodrain

import (
	"fmt"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/statusservice"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// AutoDrainReason is the audit-event reason recorded against every
// auto-drained backend.
const AutoDrainReason = "auto-drained by policy"

// AuditEventKind is the audit event kind emitted when this service
// drains a backend.
const AuditEventKind = "backend_auto_drained"

// Settings carries the policy thresholds. The caller (typically the
// pool-controller policy worker) reads these from config.Policy on
// each tick.
type Settings struct {
	// FailureRateThreshold is the per-offering failure rate above which
	// a backend is drained. A value of 0.5 means a backend whose recent
	// routable outcomes are >50% backend_failure gets drained.
	FailureRateThreshold float64
	// MinSamples is the minimum number of routable outcomes in the
	// rolling window required before the rule applies. Brand-new or
	// quiet backends are skipped.
	MinSamples int
}

// Summary captures what a single RunOnce invocation did.
type Summary struct {
	Scanned       int                `json:"scanned"`
	Drained       int                `json:"drained"`
	Skipped       int                `json:"skipped"`
	DrainedIDs    []string           `json:"drained_ids,omitempty"`
	DrainedReason map[string]float64 `json:"drained_reason,omitempty"`
}

// RunOnce evaluates the auto-drain rule against current state. Backends
// are listed once; their per-offering selection states are aggregated
// to find the worst-case failure rate. If that rate exceeds the
// threshold and the backend is currently active, it is drained.
func RunOnce(stateRepo *repo.StateRepo, settings Settings, now time.Time) (Summary, error) {
	var summary Summary
	if stateRepo == nil {
		return summary, fmt.Errorf("state repo is required")
	}
	if settings.FailureRateThreshold <= 0 || settings.FailureRateThreshold > 1 {
		return summary, fmt.Errorf("failure rate threshold must be in (0, 1]; got %v", settings.FailureRateThreshold)
	}
	backends, err := stateRepo.ListMemberBackends()
	if err != nil {
		return summary, fmt.Errorf("list member backends: %w", err)
	}
	states, err := stateRepo.ListBackendSelectionStates()
	if err != nil {
		return summary, fmt.Errorf("list backend selection states: %w", err)
	}
	worst := worstFailureRateByBackend(states, settings.MinSamples)
	for _, backend := range backends {
		summary.Scanned++
		if backend.Status != types.BackendStatusActive {
			summary.Skipped++
			continue
		}
		rate, ok := worst[backend.ID]
		if !ok {
			summary.Skipped++
			continue
		}
		if rate < settings.FailureRateThreshold {
			summary.Skipped++
			continue
		}
		if _, err := statusservice.SetBackendStatus(stateRepo, backend.ID, string(types.BackendStatusDraining)); err != nil {
			continue
		}
		summary.Drained++
		summary.DrainedIDs = append(summary.DrainedIDs, backend.ID)
		if summary.DrainedReason == nil {
			summary.DrainedReason = make(map[string]float64)
		}
		summary.DrainedReason[backend.ID] = rate
		_ = stateRepo.AppendAuditEvent(types.AuditEvent{
			Kind:         AuditEventKind,
			OccurredAt:   now,
			ResourceID:   backend.ID,
			ResourceType: "member_backend",
			Details: map[string]any{
				"reason":               AutoDrainReason,
				"failure_rate":         rate,
				"failure_rate_threshold": settings.FailureRateThreshold,
			},
		})
	}
	return summary, nil
}

// worstFailureRateByBackend returns, for each backend_id, the worst
// per-(capability,offering) failure rate observed in its
// BackendSelectionState entries that have at least minSamples routable
// outcomes in the recent window. Entries with fewer samples are
// ignored. A backend with no qualifying entries is absent from the
// returned map.
func worstFailureRateByBackend(states []types.BackendSelectionState, minSamples int) map[string]float64 {
	worst := make(map[string]float64)
	for _, st := range states {
		if st.RecentRoutableOutcomeCount < minSamples {
			continue
		}
		if st.RecentRoutableOutcomeCount == 0 {
			continue
		}
		rate := float64(st.RecentBackendFailureCount) / float64(st.RecentRoutableOutcomeCount)
		if cur, ok := worst[st.BackendID]; !ok || rate > cur {
			worst[st.BackendID] = rate
		}
	}
	return worst
}
