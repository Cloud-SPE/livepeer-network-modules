package types

import "time"

const (
	BackendSelectionStateEligible    = "eligible"
	BackendSelectionStateDegraded    = "degraded"
	BackendSelectionStateExcluded    = "excluded"
	BackendSelectionStateQuarantined = "quarantined"
)

type BackendSelectionState struct {
	Key                          string     `json:"key"`
	MemberEthAddress             string     `json:"member_eth_address"`
	BackendID                    string     `json:"backend_id"`
	CapabilityID                 string     `json:"capability_id"`
	OfferingID                   string     `json:"offering_id"`
	State                        string     `json:"state"`
	ExclusionReason              string     `json:"exclusion_reason,omitempty"`
	RoutingReason                string     `json:"routing_reason,omitempty"`
	SyntheticConfidence          float64    `json:"synthetic_confidence"`
	RealSuccessScore             float64    `json:"real_success_score"`
	RealLatencyScore             float64    `json:"real_latency_score"`
	AutomaticWarmup              bool       `json:"automatic_warmup,omitempty"`
	WarmupOverride               *float64   `json:"warmup_override,omitempty"`
	WarmupSource                 string     `json:"warmup_source,omitempty"`
	WarmupModifier               float64    `json:"warmup_modifier"`
	EffectiveSelectionScore      float64    `json:"effective_selection_score"`
	ConsecutiveSyntheticFailures uint64     `json:"consecutive_synthetic_failures"`
	CooldownUntil                *time.Time `json:"cooldown_until,omitempty"`
	MaxShareCap                  float64    `json:"max_share_cap"`
	RecentOutcomeCount           int        `json:"recent_outcome_count,omitempty"`
	RecentRoutableOutcomeCount   int        `json:"recent_routable_outcome_count,omitempty"`
	RecentBackendFailureCount    int        `json:"recent_backend_failure_count,omitempty"`
	RecentWindowStartedAt        *time.Time `json:"recent_window_started_at,omitempty"`
	RecentWindowEndedAt          *time.Time `json:"recent_window_ended_at,omitempty"`
	RecentWindowAgeSeconds       float64    `json:"recent_window_age_seconds,omitempty"`
	LastSyntheticResult          string     `json:"last_synthetic_result,omitempty"`
	LastSyntheticAt              *time.Time `json:"last_synthetic_at,omitempty"`
	LastRealOutcomeAt            *time.Time `json:"last_real_outcome_at,omitempty"`
	CreatedAt                    time.Time  `json:"created_at"`
	UpdatedAt                    time.Time  `json:"updated_at"`
}

type BackendSelectionSnapshot struct {
	GeneratedAt                   time.Time               `json:"generated_at"`
	Version                       int                     `json:"version"`
	CooldownDurationSeconds       float64                 `json:"cooldown_duration_seconds,omitempty"`
	CooldownFailureTrigger        int                     `json:"cooldown_failure_trigger,omitempty"`
	EMAHalfLifeSeconds            float64                 `json:"ema_half_life_seconds,omitempty"`
	LatencyTargetMS               float64                 `json:"latency_target_ms,omitempty"`
	RecentWindowStaleAfterSeconds float64                 `json:"recent_window_stale_after_seconds,omitempty"`
	WindowScoreWeight             float64                 `json:"window_score_weight,omitempty"`
	EMAScoreWeight                float64                 `json:"ema_score_weight,omitempty"`
	WarmupModifier                float64                 `json:"warmup_modifier,omitempty"`
	WarmupExitSamples             int                     `json:"warmup_exit_samples,omitempty"`
	Entries                       []BackendSelectionState `json:"entries"`
}
