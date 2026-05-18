package registry

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/poolsnapshot"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/selection"
)

type MetadataStatusSource interface {
	StatusFor(capabilityID, offeringID string) (MetadataStatus, bool)
}

type PoolStatusSource interface {
	StatusFor(backendID, capabilityID, offeringID string) poolsnapshot.Status
}

type MetadataStatus struct {
	Provider            string
	Applicable          bool
	LastAttemptAt       time.Time
	LastSuccessAt       time.Time
	LastError           string
	LastResult          string
	ConsecutiveFailures int
}

type healthResponse struct {
	BrokerStatus string                   `json:"broker_status"`
	GeneratedAt  time.Time                `json:"generated_at"`
	Capabilities []healthCapabilityStatus `json:"capabilities"`
}

type healthCapabilityStatus struct {
	ID                   string               `json:"id"`
	OfferingID           string               `json:"offering_id"`
	Status               health.Status        `json:"status"`
	Reason               string               `json:"reason,omitempty"`
	ProbeType            string               `json:"probe_type,omitempty"`
	ProbedAt             time.Time            `json:"probed_at,omitempty"`
	StaleAfter           time.Time            `json:"stale_after,omitempty"`
	ConsecutiveSuccesses int                  `json:"consecutive_successes,omitempty"`
	ConsecutiveFailures  int                  `json:"consecutive_failures,omitempty"`
	Backends             []backendStatus      `json:"backends,omitempty"`
	Pool                 *poolAggregateStatus `json:"pool,omitempty"`
	Metadata             *metadataStatus      `json:"metadata,omitempty"`
}

type backendStatus struct {
	BackendID            string        `json:"backend_id,omitempty"`
	Status               health.Status `json:"status"`
	Reason               string        `json:"reason,omitempty"`
	ProbeType            string        `json:"probe_type,omitempty"`
	ProbedAt             time.Time     `json:"probed_at,omitempty"`
	StaleAfter           time.Time     `json:"stale_after,omitempty"`
	ConsecutiveSuccesses int           `json:"consecutive_successes,omitempty"`
	ConsecutiveFailures  int           `json:"consecutive_failures,omitempty"`
	SelectionEligible    bool          `json:"selection_eligible"`
	SelectionWeight      int           `json:"selection_weight,omitempty"`
	SelectionReason      string        `json:"selection_reason,omitempty"`
	Pool                 *poolStatus   `json:"pool,omitempty"`
}

type metadataStatus struct {
	Provider              string    `json:"provider,omitempty"`
	Applicable            bool      `json:"applicable"`
	LastAttemptAt         time.Time `json:"last_attempt_at,omitempty"`
	LastSuccessAt         time.Time `json:"last_success_at,omitempty"`
	LastSuccessAgeSeconds float64   `json:"last_success_age_seconds,omitempty"`
	LastError             string    `json:"last_error,omitempty"`
	LastResult            string    `json:"last_result,omitempty"`
	ConsecutiveFailures   int       `json:"consecutive_failures,omitempty"`
}

type poolStatus struct {
	SnapshotStatus                        string    `json:"snapshot_status"`
	SnapshotGeneratedAt                   time.Time `json:"snapshot_generated_at,omitempty"`
	SnapshotFetchedAt                     time.Time `json:"snapshot_fetched_at,omitempty"`
	SnapshotAgeSeconds                    float64   `json:"snapshot_age_seconds,omitempty"`
	SnapshotTimeoutSeconds                float64   `json:"snapshot_timeout_seconds,omitempty"`
	SnapshotPollIntervalSeconds           float64   `json:"snapshot_poll_interval_seconds,omitempty"`
	SnapshotStaleAfterSeconds             float64   `json:"snapshot_stale_after_seconds,omitempty"`
	SnapshotExpireAfterSeconds            float64   `json:"snapshot_expire_after_seconds,omitempty"`
	SnapshotCooldownDurationSeconds       float64   `json:"snapshot_cooldown_duration_seconds,omitempty"`
	SnapshotCooldownFailureTrigger        int       `json:"snapshot_cooldown_failure_trigger,omitempty"`
	SnapshotEMAHalfLifeSeconds            float64   `json:"snapshot_ema_half_life_seconds,omitempty"`
	SnapshotLatencyTargetMS               float64   `json:"snapshot_latency_target_ms,omitempty"`
	SnapshotRecentWindowStaleAfterSeconds float64   `json:"snapshot_recent_window_stale_after_seconds,omitempty"`
	SnapshotWindowScoreWeight             float64   `json:"snapshot_window_score_weight,omitempty"`
	SnapshotEMAScoreWeight                float64   `json:"snapshot_ema_score_weight,omitempty"`
	SnapshotWarmupModifier                float64   `json:"snapshot_warmup_modifier,omitempty"`
	SnapshotWarmupExitSamples             int       `json:"snapshot_warmup_exit_samples,omitempty"`
	EntryFound                            bool      `json:"entry_found"`
	State                                 string    `json:"state,omitempty"`
	ExclusionReason                       string    `json:"exclusion_reason,omitempty"`
	RoutingReason                         string    `json:"routing_reason,omitempty"`
	SyntheticConfidence                   float64   `json:"synthetic_confidence,omitempty"`
	RealSuccessScore                      float64   `json:"real_success_score,omitempty"`
	RealLatencyScore                      float64   `json:"real_latency_score,omitempty"`
	EffectiveSelectionScore               float64   `json:"effective_selection_score,omitempty"`
	ConsecutiveSyntheticFailures          uint64    `json:"consecutive_synthetic_failures,omitempty"`
	CooldownUntil                         time.Time `json:"cooldown_until,omitempty"`
	AutomaticWarmup                       bool      `json:"automatic_warmup,omitempty"`
	WarmupOverride                        *float64  `json:"warmup_override,omitempty"`
	WarmupSource                          string    `json:"warmup_source,omitempty"`
	WarmupModifier                        float64   `json:"warmup_modifier,omitempty"`
	MaxShareCap                           float64   `json:"max_share_cap,omitempty"`
	RecentOutcomeCount                    int       `json:"recent_outcome_count,omitempty"`
	RecentRoutableOutcomeCount            int       `json:"recent_routable_outcome_count,omitempty"`
	RecentBackendFailureCount             int       `json:"recent_backend_failure_count,omitempty"`
	RecentWindowStartedAt                 time.Time `json:"recent_window_started_at,omitempty"`
	RecentWindowEndedAt                   time.Time `json:"recent_window_ended_at,omitempty"`
	RecentWindowAgeSeconds                float64   `json:"recent_window_age_seconds,omitempty"`
	LastSyntheticResult                   string    `json:"last_synthetic_result,omitempty"`
	LastSyntheticAt                       time.Time `json:"last_synthetic_at,omitempty"`
	LastRealOutcomeAt                     time.Time `json:"last_real_outcome_at,omitempty"`
	LastError                             string    `json:"last_error,omitempty"`
}

type poolAggregateStatus struct {
	Configured                            bool           `json:"configured"`
	SnapshotStatus                        string         `json:"snapshot_status,omitempty"`
	SnapshotTimeoutSeconds                float64        `json:"snapshot_timeout_seconds,omitempty"`
	SnapshotPollIntervalSeconds           float64        `json:"snapshot_poll_interval_seconds,omitempty"`
	SnapshotStaleAfterSeconds             float64        `json:"snapshot_stale_after_seconds,omitempty"`
	SnapshotExpireAfterSeconds            float64        `json:"snapshot_expire_after_seconds,omitempty"`
	SnapshotCooldownDurationSeconds       float64        `json:"snapshot_cooldown_duration_seconds,omitempty"`
	SnapshotCooldownFailureTrigger        int            `json:"snapshot_cooldown_failure_trigger,omitempty"`
	SnapshotEMAHalfLifeSeconds            float64        `json:"snapshot_ema_half_life_seconds,omitempty"`
	SnapshotLatencyTargetMS               float64        `json:"snapshot_latency_target_ms,omitempty"`
	SnapshotRecentWindowStaleAfterSeconds float64        `json:"snapshot_recent_window_stale_after_seconds,omitempty"`
	SnapshotWindowScoreWeight             float64        `json:"snapshot_window_score_weight,omitempty"`
	SnapshotEMAScoreWeight                float64        `json:"snapshot_ema_score_weight,omitempty"`
	SnapshotWarmupModifier                float64        `json:"snapshot_warmup_modifier,omitempty"`
	SnapshotWarmupExitSamples             int            `json:"snapshot_warmup_exit_samples,omitempty"`
	BackendCount                          int            `json:"backend_count"`
	EntryFoundCount                       int            `json:"entry_found_count,omitempty"`
	EligibleCount                         int            `json:"eligible_count,omitempty"`
	DegradedCount                         int            `json:"degraded_count,omitempty"`
	ExcludedCount                         int            `json:"excluded_count,omitempty"`
	QuarantinedCount                      int            `json:"quarantined_count,omitempty"`
	AutomaticWarmupCount                  int            `json:"automatic_warmup_count,omitempty"`
	AverageEffectiveScore                 float64        `json:"average_effective_selection_score,omitempty"`
	AverageSyntheticConfidence            float64        `json:"average_synthetic_confidence,omitempty"`
	AverageRealSuccessScore               float64        `json:"average_real_success_score,omitempty"`
	AverageRealLatencyScore               float64        `json:"average_real_latency_score,omitempty"`
	RecentOutcomeCount                    int            `json:"recent_outcome_count,omitempty"`
	RecentRoutableOutcomeCount            int            `json:"recent_routable_outcome_count,omitempty"`
	RecentBackendFailureCount             int            `json:"recent_backend_failure_count,omitempty"`
	RecentWindowStartedAt                 time.Time      `json:"recent_window_started_at,omitempty"`
	RecentWindowEndedAt                   time.Time      `json:"recent_window_ended_at,omitempty"`
	AverageRecentWindowAgeSeconds         float64        `json:"average_recent_window_age_seconds,omitempty"`
	TopRoutingReasons                     map[string]int `json:"top_routing_reasons,omitempty"`
	TopExclusionReasons                   map[string]int `json:"top_exclusion_reasons,omitempty"`
}

// HealthHandler returns the broker's normalized live-health snapshot.
func HealthHandler(mgr *health.Manager, metadata MetadataStatusSource, pool PoolStatusSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteHealthResponse(w, mgr, metadata, pool)
	}
}

func WriteHealthResponse(w http.ResponseWriter, mgr *health.Manager, metadata MetadataStatusSource, pool PoolStatusSource) {
	snap := mgr.Snapshot()
	statuses := make(map[string]string, len(snap.Capabilities))
	grouped := make(map[string]*healthCapabilityStatus, len(snap.Capabilities))
	out := healthResponse{
		BrokerStatus: snap.BrokerStatus,
		GeneratedAt:  snap.GeneratedAt,
		Capabilities: make([]healthCapabilityStatus, 0, len(snap.Capabilities)),
	}
	for _, cap := range snap.Capabilities {
		key := cap.ID + "|" + cap.OfferingID
		entry, ok := grouped[key]
		if !ok {
			grouped[key] = &healthCapabilityStatus{
				ID:         cap.ID,
				OfferingID: cap.OfferingID,
				Status:     cap.Status,
				Reason:     cap.Reason,
				ProbeType:  cap.ProbeType,
				ProbedAt:   cap.ProbedAt,
				StaleAfter: cap.StaleAfter,
				Backends:   make([]backendStatus, 0, 1),
			}
			entry = grouped[key]
			if st, ok := metadata.StatusFor(cap.ID, cap.OfferingID); ok {
				lastSuccessAgeSeconds := 0.0
				if st.LastSuccessAt.IsZero() {
					lastSuccessAgeSeconds = -1
				} else {
					lastSuccessAgeSeconds = out.GeneratedAt.Sub(st.LastSuccessAt).Seconds()
				}
				entry.Metadata = &metadataStatus{
					Provider:              st.Provider,
					Applicable:            st.Applicable,
					LastAttemptAt:         st.LastAttemptAt,
					LastSuccessAt:         st.LastSuccessAt,
					LastSuccessAgeSeconds: lastSuccessAgeSeconds,
					LastError:             st.LastError,
					LastResult:            st.LastResult,
					ConsecutiveFailures:   st.ConsecutiveFailures,
				}
			}
		}
		var poolStatusValue *poolsnapshot.Status
		if pool != nil {
			if ps := pool.StatusFor(cap.BackendID, cap.ID, cap.OfferingID); ps.Configured {
				poolStatusValue = &ps
			}
		}
		decision := selection.DecisionFor(cap, poolStatusValue)
		backend := backendStatus{
			BackendID:            cap.BackendID,
			Status:               cap.Status,
			Reason:               cap.Reason,
			ProbeType:            cap.ProbeType,
			ProbedAt:             cap.ProbedAt,
			StaleAfter:           cap.StaleAfter,
			ConsecutiveSuccesses: cap.ConsecutiveSuccesses,
			ConsecutiveFailures:  cap.ConsecutiveFailures,
			SelectionEligible:    decision.Eligible,
			SelectionWeight:      decision.Weight,
			SelectionReason:      decision.Reason,
		}
		if poolStatusValue != nil {
			ps := *poolStatusValue
			backend.Pool = &poolStatus{
				SnapshotStatus:                        ps.SnapshotStatus,
				SnapshotGeneratedAt:                   ps.SnapshotGeneratedAt,
				SnapshotFetchedAt:                     ps.SnapshotFetchedAt,
				SnapshotAgeSeconds:                    ps.SnapshotAgeSeconds,
				SnapshotTimeoutSeconds:                ps.SnapshotTimeoutSeconds,
				SnapshotPollIntervalSeconds:           ps.SnapshotPollIntervalSeconds,
				SnapshotStaleAfterSeconds:             ps.SnapshotStaleAfterSeconds,
				SnapshotExpireAfterSeconds:            ps.SnapshotExpireAfterSeconds,
				SnapshotCooldownDurationSeconds:       ps.SnapshotCooldownDurationSeconds,
				SnapshotCooldownFailureTrigger:        ps.SnapshotCooldownFailureTrigger,
				SnapshotEMAHalfLifeSeconds:            ps.SnapshotEMAHalfLifeSeconds,
				SnapshotLatencyTargetMS:               ps.SnapshotLatencyTargetMS,
				SnapshotRecentWindowStaleAfterSeconds: ps.SnapshotRecentWindowStaleAfterSeconds,
				SnapshotWindowScoreWeight:             ps.SnapshotWindowScoreWeight,
				SnapshotEMAScoreWeight:                ps.SnapshotEMAScoreWeight,
				SnapshotWarmupModifier:                ps.SnapshotWarmupModifier,
				SnapshotWarmupExitSamples:             ps.SnapshotWarmupExitSamples,
				EntryFound:                            ps.EntryFound,
				State:                                 ps.EntryState,
				ExclusionReason:                       ps.EntryExclusionReason,
				RoutingReason:                         ps.EntryRoutingReason,
				SyntheticConfidence:                   ps.EntrySyntheticConfidence,
				RealSuccessScore:                      ps.EntryRealSuccessScore,
				RealLatencyScore:                      ps.EntryRealLatencyScore,
				EffectiveSelectionScore:               ps.EntryEffectiveSelectionScore,
				ConsecutiveSyntheticFailures:          ps.EntryConsecutiveSyntheticFailures,
				CooldownUntil:                         ps.EntryCooldownUntil,
				AutomaticWarmup:                       ps.EntryAutomaticWarmup,
				WarmupOverride:                        ps.EntryWarmupOverride,
				WarmupSource:                          ps.EntryWarmupSource,
				WarmupModifier:                        ps.EntryWarmupModifier,
				MaxShareCap:                           ps.EntryMaxShareCap,
				RecentOutcomeCount:                    ps.EntryRecentOutcomeCount,
				RecentRoutableOutcomeCount:            ps.EntryRecentRoutableOutcomeCount,
				RecentBackendFailureCount:             ps.EntryRecentBackendFailureCount,
				RecentWindowStartedAt:                 ps.EntryRecentWindowStartedAt,
				RecentWindowEndedAt:                   ps.EntryRecentWindowEndedAt,
				RecentWindowAgeSeconds:                ps.EntryRecentWindowAgeSeconds,
				LastSyntheticResult:                   ps.EntryLastSyntheticResult,
				LastSyntheticAt:                       ps.EntryLastSyntheticAt,
				LastRealOutcomeAt:                     ps.EntryLastRealOutcomeAt,
				LastError:                             ps.LastError,
			}
		}
		entry.Backends = append(entry.Backends, backend)
		entry.Status = aggregateStatus(entry.Backends)
		entry.Reason = aggregateReason(entry.Backends)
		statuses[key] = string(entry.Status)
	}
	for _, entry := range grouped {
		entry.Pool = aggregatePool(entry.Backends)
		out.Capabilities = append(out.Capabilities, *entry)
	}
	sort.Slice(out.Capabilities, func(i, j int) bool {
		if out.Capabilities[i].ID != out.Capabilities[j].ID {
			return out.Capabilities[i].ID < out.Capabilities[j].ID
		}
		return out.Capabilities[i].OfferingID < out.Capabilities[j].OfferingID
	})
	statusesJSON, _ := json.Marshal(statuses)
	w.Header().Set(livepeerheader.HealthStatus, string(statusesJSON))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(out)
}

func aggregatePool(backends []backendStatus) *poolAggregateStatus {
	if len(backends) == 0 {
		return nil
	}
	out := &poolAggregateStatus{
		TopRoutingReasons:   map[string]int{},
		TopExclusionReasons: map[string]int{},
	}
	configuredBackends := 0
	scoreCount := 0
	snapshotStatusRank := -1
	var latestWindowEnd time.Time
	var earliestWindowStart time.Time
	windowAgeCount := 0
	for _, backend := range backends {
		if backend.Pool == nil {
			continue
		}
		configuredBackends++
		out.Configured = true
		if backend.Pool.SnapshotTimeoutSeconds > 0 {
			out.SnapshotTimeoutSeconds = backend.Pool.SnapshotTimeoutSeconds
		}
		if backend.Pool.SnapshotPollIntervalSeconds > 0 {
			out.SnapshotPollIntervalSeconds = backend.Pool.SnapshotPollIntervalSeconds
		}
		if backend.Pool.SnapshotStaleAfterSeconds > 0 {
			out.SnapshotStaleAfterSeconds = backend.Pool.SnapshotStaleAfterSeconds
		}
		if backend.Pool.SnapshotExpireAfterSeconds > 0 {
			out.SnapshotExpireAfterSeconds = backend.Pool.SnapshotExpireAfterSeconds
		}
		if backend.Pool.SnapshotCooldownDurationSeconds > 0 {
			out.SnapshotCooldownDurationSeconds = backend.Pool.SnapshotCooldownDurationSeconds
		}
		if backend.Pool.SnapshotCooldownFailureTrigger > 0 {
			out.SnapshotCooldownFailureTrigger = backend.Pool.SnapshotCooldownFailureTrigger
		}
		if backend.Pool.SnapshotEMAHalfLifeSeconds > 0 {
			out.SnapshotEMAHalfLifeSeconds = backend.Pool.SnapshotEMAHalfLifeSeconds
		}
		if backend.Pool.SnapshotLatencyTargetMS > 0 {
			out.SnapshotLatencyTargetMS = backend.Pool.SnapshotLatencyTargetMS
		}
		if backend.Pool.SnapshotRecentWindowStaleAfterSeconds > 0 {
			out.SnapshotRecentWindowStaleAfterSeconds = backend.Pool.SnapshotRecentWindowStaleAfterSeconds
		}
		if backend.Pool.SnapshotWindowScoreWeight > 0 {
			out.SnapshotWindowScoreWeight = backend.Pool.SnapshotWindowScoreWeight
		}
		if backend.Pool.SnapshotEMAScoreWeight > 0 {
			out.SnapshotEMAScoreWeight = backend.Pool.SnapshotEMAScoreWeight
		}
		if backend.Pool.SnapshotWarmupModifier > 0 {
			out.SnapshotWarmupModifier = backend.Pool.SnapshotWarmupModifier
		}
		if backend.Pool.SnapshotWarmupExitSamples > 0 {
			out.SnapshotWarmupExitSamples = backend.Pool.SnapshotWarmupExitSamples
		}
		out.BackendCount++
		if backend.Pool.EntryFound {
			out.EntryFoundCount++
		}
		switch backend.Pool.State {
		case "eligible":
			out.EligibleCount++
		case "degraded":
			out.DegradedCount++
		case "excluded":
			out.ExcludedCount++
		case "quarantined":
			out.QuarantinedCount++
		}
		if backend.Pool.AutomaticWarmup {
			out.AutomaticWarmupCount++
		}
		out.AverageEffectiveScore += backend.Pool.EffectiveSelectionScore
		out.AverageSyntheticConfidence += backend.Pool.SyntheticConfidence
		out.AverageRealSuccessScore += backend.Pool.RealSuccessScore
		out.AverageRealLatencyScore += backend.Pool.RealLatencyScore
		out.RecentOutcomeCount += backend.Pool.RecentOutcomeCount
		out.RecentRoutableOutcomeCount += backend.Pool.RecentRoutableOutcomeCount
		out.RecentBackendFailureCount += backend.Pool.RecentBackendFailureCount
		if !backend.Pool.RecentWindowStartedAt.IsZero() {
			if earliestWindowStart.IsZero() || backend.Pool.RecentWindowStartedAt.Before(earliestWindowStart) {
				earliestWindowStart = backend.Pool.RecentWindowStartedAt
			}
		}
		if !backend.Pool.RecentWindowEndedAt.IsZero() {
			if latestWindowEnd.IsZero() || backend.Pool.RecentWindowEndedAt.After(latestWindowEnd) {
				latestWindowEnd = backend.Pool.RecentWindowEndedAt
			}
		}
		out.AverageRecentWindowAgeSeconds += backend.Pool.RecentWindowAgeSeconds
		windowAgeCount++
		scoreCount++
		if reason := backend.Pool.RoutingReason; reason != "" {
			out.TopRoutingReasons[reason]++
		}
		if reason := backend.Pool.ExclusionReason; reason != "" {
			out.TopExclusionReasons[reason]++
		}
		rank := poolSnapshotStatusRank(backend.Pool.SnapshotStatus)
		if rank > snapshotStatusRank {
			snapshotStatusRank = rank
			out.SnapshotStatus = backend.Pool.SnapshotStatus
		}
	}
	if configuredBackends == 0 {
		return nil
	}
	if scoreCount > 0 {
		divisor := float64(scoreCount)
		out.AverageEffectiveScore /= divisor
		out.AverageSyntheticConfidence /= divisor
		out.AverageRealSuccessScore /= divisor
		out.AverageRealLatencyScore /= divisor
	}
	if windowAgeCount > 0 {
		out.AverageRecentWindowAgeSeconds /= float64(windowAgeCount)
	}
	out.RecentWindowStartedAt = earliestWindowStart
	out.RecentWindowEndedAt = latestWindowEnd
	if len(out.TopExclusionReasons) == 0 {
		out.TopExclusionReasons = nil
	}
	if len(out.TopRoutingReasons) == 0 {
		out.TopRoutingReasons = nil
	}
	return out
}

func poolSnapshotStatusRank(status string) int {
	switch status {
	case "expired":
		return 4
	case "fetch_error":
		return 3
	case "bootstrap_pending":
		return 2
	case "stale":
		return 1
	case "fresh":
		return 0
	default:
		return -1
	}
}

func aggregateStatus(backends []backendStatus) health.Status {
	if len(backends) == 0 {
		return health.StatusStale
	}
	eligible := selectableBackends(backends)
	if len(eligible) > 0 {
		return aggregateBackendProbeStatus(eligible)
	}
	if hasPoolSnapshotBlock(backends) {
		return health.StatusStale
	}
	if hasPoolRoutingBlock(backends) {
		return health.StatusDegraded
	}
	return aggregateBackendProbeStatus(backends)
}

func aggregateBackendProbeStatus(backends []backendStatus) health.Status {
	if len(backends) == 0 {
		return health.StatusStale
	}
	best := health.StatusUnreachable
	for _, backend := range backends {
		switch backend.Status {
		case health.StatusReady:
			return health.StatusReady
		case health.StatusDegraded:
			best = health.StatusDegraded
		case health.StatusDraining:
			if best != health.StatusDegraded {
				best = health.StatusDraining
			}
		case health.StatusStale:
			if best != health.StatusDegraded && best != health.StatusDraining {
				best = health.StatusStale
			}
		}
	}
	return best
}

func aggregateReason(backends []backendStatus) string {
	eligible := selectableBackends(backends)
	if len(eligible) > 0 {
		return aggregateBackendProbeReason(eligible)
	}
	for _, backend := range backends {
		if backend.SelectionReason != "" {
			return backend.SelectionReason
		}
	}
	return aggregateBackendProbeReason(backends)
}

func aggregateBackendProbeReason(backends []backendStatus) string {
	for _, backend := range backends {
		if backend.Status == health.StatusReady || backend.Status == health.StatusDegraded {
			return backend.Reason
		}
	}
	if len(backends) == 0 {
		return ""
	}
	return backends[0].Reason
}

func selectableBackends(backends []backendStatus) []backendStatus {
	out := make([]backendStatus, 0, len(backends))
	for _, backend := range backends {
		if backend.SelectionEligible {
			out = append(out, backend)
		}
	}
	return out
}

func hasPoolSnapshotBlock(backends []backendStatus) bool {
	for _, backend := range backends {
		if backend.SelectionReason == "pool_snapshot_entry_missing" || strings.HasPrefix(backend.SelectionReason, "pool_snapshot_") {
			return true
		}
	}
	return false
}

func hasPoolRoutingBlock(backends []backendStatus) bool {
	for _, backend := range backends {
		if backend.SelectionReason == "" {
			continue
		}
		if strings.HasPrefix(backend.SelectionReason, "pool_") || backend.SelectionReason == "score_below_floor" || backend.SelectionReason == "synthetic_probe_failure_threshold" {
			return true
		}
	}
	return false
}
