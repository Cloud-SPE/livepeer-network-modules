package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/poolsnapshot"
)

type stubPoolStatusSource struct {
	status map[string]poolsnapshot.Status
}

func (s stubPoolStatusSource) StatusFor(backendID, capabilityID, offeringID string) poolsnapshot.Status {
	return s.status[backendID+"|"+capabilityID+"|"+offeringID]
}

// healthHandler serves one prepared verdict the way the broker's route
// does. WriteHealthResponse owns the wire shape and takes the verdict as
// input; where health comes from — attached runners now, a prober once —
// is the caller's business and deliberately not exercised here.
func healthHandler(snap health.Response, pool PoolStatusSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteHealthResponse(w, snap, pool)
	}
}

// verdict assembles the whole-broker response from per-backend
// snapshots, deriving broker_status through the same aggregation the
// route applies so these fixtures cannot drift from production.
func verdict(snaps ...health.Snapshot) health.Response {
	return health.Response{
		BrokerStatus: string(health.BrokerStatus(snaps)),
		GeneratedAt:  time.Now().UTC(),
		Capabilities: snaps,
	}
}

// readySnapshot is a backend the broker considers fit to serve. These
// tests are about what the pool overlay does to such a backend, so the
// backend's own verdict is held constant at ready.
func readySnapshot(capabilityID, offeringID, backendID string) health.Snapshot {
	return health.Snapshot{
		ID:         capabilityID,
		OfferingID: offeringID,
		BackendID:  backendID,
		Status:     health.StatusReady,
		Reason:     "initial_status",
	}
}

func TestHealthHandler_EmbedsPoolSnapshotStatusWhenConfigured(t *testing.T) {
	snap := verdict(readySnapshot("openai:chat-completions", "default", "backend-a"))

	req := httptest.NewRequest(http.MethodGet, "/registry/health", nil)
	rec := httptest.NewRecorder()
	healthHandler(snap, stubPoolStatusSource{
		status: map[string]poolsnapshot.Status{
			"backend-a|openai:chat-completions|default": {
				Configured:                            true,
				SnapshotStatus:                        "fresh",
				SnapshotGeneratedAt:                   time.Date(2026, 5, 17, 16, 20, 0, 0, time.UTC),
				SnapshotFetchedAt:                     time.Date(2026, 5, 17, 16, 20, 1, 0, time.UTC),
				SnapshotAgeSeconds:                    3,
				SnapshotTimeoutSeconds:                0.1,
				SnapshotPollIntervalSeconds:           1,
				SnapshotStaleAfterSeconds:             15,
				SnapshotExpireAfterSeconds:            60,
				SnapshotCooldownDurationSeconds:       300,
				SnapshotCooldownFailureTrigger:        5,
				SnapshotEMAHalfLifeSeconds:            86400,
				SnapshotLatencyTargetMS:               1200,
				SnapshotRecentWindowStaleAfterSeconds: 300,
				SnapshotWindowScoreWeight:             0.7,
				SnapshotEMAScoreWeight:                0.3,
				SnapshotWarmupModifier:                0.25,
				SnapshotWarmupExitSamples:             20,
				EntryFound:                            true,
				EntryState:                            "eligible",
				EntryRoutingReason:                    "pool_warmup",
				EntrySyntheticConfidence:              0.8,
				EntryRealSuccessScore:                 0.9,
				EntryRealLatencyScore:                 0.7,
				EntryEffectiveSelectionScore:          0.5,
				EntryConsecutiveSyntheticFailures:     1,
				EntryCooldownUntil:                    time.Date(2026, 5, 17, 16, 25, 0, 0, time.UTC),
				EntryAutomaticWarmup:                  true,
				EntryWarmupSource:                     "automatic",
				EntryRecentOutcomeCount:               7,
				EntryRecentRoutableOutcomeCount:       6,
				EntryRecentBackendFailureCount:        2,
				EntryRecentWindowStartedAt:            time.Date(2026, 5, 17, 16, 14, 0, 0, time.UTC),
				EntryRecentWindowEndedAt:              time.Date(2026, 5, 17, 16, 19, 0, 0, time.UTC),
				EntryRecentWindowAgeSeconds:           60,
				EntryLastSyntheticResult:              "probe_ok",
				EntryLastSyntheticAt:                  time.Date(2026, 5, 17, 16, 19, 0, 0, time.UTC),
				EntryLastRealOutcomeAt:                time.Date(2026, 5, 17, 16, 18, 0, 0, time.UTC),
			},
		},
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want %d", rec.Code, http.StatusOK)
	}

	var out struct {
		Capabilities []struct {
			Backends []struct {
				SelectionEligible bool   `json:"selection_eligible"`
				SelectionWeight   int    `json:"selection_weight"`
				SelectionReason   string `json:"selection_reason"`
				Pool              *struct {
					SnapshotStatus                        string  `json:"snapshot_status"`
					SnapshotAgeSeconds                    float64 `json:"snapshot_age_seconds"`
					SnapshotTimeoutSeconds                float64 `json:"snapshot_timeout_seconds"`
					SnapshotPollIntervalSeconds           float64 `json:"snapshot_poll_interval_seconds"`
					SnapshotStaleAfterSeconds             float64 `json:"snapshot_stale_after_seconds"`
					SnapshotExpireAfterSeconds            float64 `json:"snapshot_expire_after_seconds"`
					SnapshotCooldownDurationSeconds       float64 `json:"snapshot_cooldown_duration_seconds"`
					SnapshotCooldownFailureTrigger        int     `json:"snapshot_cooldown_failure_trigger"`
					SnapshotEMAHalfLifeSeconds            float64 `json:"snapshot_ema_half_life_seconds"`
					SnapshotLatencyTargetMS               float64 `json:"snapshot_latency_target_ms"`
					SnapshotRecentWindowStaleAfterSeconds float64 `json:"snapshot_recent_window_stale_after_seconds"`
					SnapshotWindowScoreWeight             float64 `json:"snapshot_window_score_weight"`
					SnapshotEMAScoreWeight                float64 `json:"snapshot_ema_score_weight"`
					SnapshotWarmupModifier                float64 `json:"snapshot_warmup_modifier"`
					SnapshotWarmupExitSamples             int     `json:"snapshot_warmup_exit_samples"`
					EntryFound                            bool    `json:"entry_found"`
					State                                 string  `json:"state"`
					SyntheticConfidence                   float64 `json:"synthetic_confidence"`
					RealSuccessScore                      float64 `json:"real_success_score"`
					RealLatencyScore                      float64 `json:"real_latency_score"`
					EffectiveSelectionScore               float64 `json:"effective_selection_score"`
					ConsecutiveSyntheticFailures          uint64  `json:"consecutive_synthetic_failures"`
					AutomaticWarmup                       bool    `json:"automatic_warmup"`
					WarmupSource                          string  `json:"warmup_source"`
					RecentOutcomeCount                    int     `json:"recent_outcome_count"`
					RecentRoutableOutcomeCount            int     `json:"recent_routable_outcome_count"`
					RecentBackendFailureCount             int     `json:"recent_backend_failure_count"`
					RecentWindowStartedAt                 string  `json:"recent_window_started_at"`
					RecentWindowEndedAt                   string  `json:"recent_window_ended_at"`
					RecentWindowAgeSeconds                float64 `json:"recent_window_age_seconds"`
					LastSyntheticResult                   string  `json:"last_synthetic_result"`
				} `json:"pool"`
			} `json:"backends"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Capabilities) != 1 || len(out.Capabilities[0].Backends) != 1 {
		t.Fatalf("unexpected backend counts: %+v", out.Capabilities)
	}
	pool := out.Capabilities[0].Backends[0].Pool
	if pool == nil {
		t.Fatal("pool = nil; want populated pool snapshot status")
	}
	if !out.Capabilities[0].Backends[0].SelectionEligible {
		t.Fatalf("selection_eligible = false; want true")
	}
	if out.Capabilities[0].Backends[0].SelectionWeight != 50 {
		t.Fatalf("selection_weight = %d; want 50", out.Capabilities[0].Backends[0].SelectionWeight)
	}
	if out.Capabilities[0].Backends[0].SelectionReason != "pool_warmup" {
		t.Fatalf("selection_reason = %q; want pool_warmup", out.Capabilities[0].Backends[0].SelectionReason)
	}
	if pool.SnapshotStatus != "fresh" {
		t.Fatalf("pool.snapshot_status = %q; want fresh", pool.SnapshotStatus)
	}
	if !pool.EntryFound {
		t.Fatal("pool.entry_found = false; want true")
	}
	if pool.State != "eligible" {
		t.Fatalf("pool.state = %q; want eligible", pool.State)
	}
	if pool.SyntheticConfidence != 0.8 || pool.RealSuccessScore != 0.9 || pool.RealLatencyScore != 0.7 {
		t.Fatalf("pool detailed scores = %+v; want synthetic=0.8 success=0.9 latency=0.7", pool)
	}
	if pool.EffectiveSelectionScore != 0.5 {
		t.Fatalf("pool.effective_selection_score = %v; want 0.5", pool.EffectiveSelectionScore)
	}
	if pool.ConsecutiveSyntheticFailures != 1 {
		t.Fatalf("pool.consecutive_synthetic_failures = %d; want 1", pool.ConsecutiveSyntheticFailures)
	}
	if !pool.AutomaticWarmup || pool.WarmupSource != "automatic" {
		t.Fatalf("pool warmup flags = %+v; want automatic warmup", pool)
	}
	if pool.RecentOutcomeCount != 7 || pool.RecentRoutableOutcomeCount != 6 || pool.RecentBackendFailureCount != 2 {
		t.Fatalf("pool recent counts = %+v; want outcome=7 routable=6 failures=2", pool)
	}
	if pool.RecentWindowStartedAt != "2026-05-17T16:14:00Z" || pool.RecentWindowEndedAt != "2026-05-17T16:19:00Z" || pool.RecentWindowAgeSeconds != 60 {
		t.Fatalf("pool recent window = %+v; want started=16:14 ended=16:19 age=60", pool)
	}
	if pool.LastSyntheticResult != "probe_ok" {
		t.Fatalf("pool.last_synthetic_result = %q; want probe_ok", pool.LastSyntheticResult)
	}
	if pool.SnapshotAgeSeconds != 3 {
		t.Fatalf("pool.snapshot_age_seconds = %v; want 3", pool.SnapshotAgeSeconds)
	}
	if pool.SnapshotTimeoutSeconds != 0.1 || pool.SnapshotPollIntervalSeconds != 1 || pool.SnapshotStaleAfterSeconds != 15 || pool.SnapshotExpireAfterSeconds != 60 {
		t.Fatalf("pool broker snapshot policy = %+v; want timeout=0.1 poll=1 stale=15 expire=60", pool)
	}
	if pool.SnapshotCooldownDurationSeconds != 300 || pool.SnapshotCooldownFailureTrigger != 5 {
		t.Fatalf("pool cooldown snapshot settings = %+v; want duration=300 trigger=5", pool)
	}
	if pool.SnapshotEMAHalfLifeSeconds != 86400 || pool.SnapshotLatencyTargetMS != 1200 {
		t.Fatalf("pool ema/latency snapshot settings = %+v; want ema=86400 latency=1200", pool)
	}
	if pool.SnapshotRecentWindowStaleAfterSeconds != 300 {
		t.Fatalf("pool.snapshot_recent_window_stale_after_seconds = %v; want 300", pool.SnapshotRecentWindowStaleAfterSeconds)
	}
	if pool.SnapshotWindowScoreWeight != 0.7 || pool.SnapshotEMAScoreWeight != 0.3 {
		t.Fatalf("pool score weights = %+v; want window=0.7 ema=0.3", pool)
	}
	if pool.SnapshotWarmupModifier != 0.25 || pool.SnapshotWarmupExitSamples != 20 {
		t.Fatalf("pool warmup snapshot settings = %+v; want modifier=0.25 exit_samples=20", pool)
	}
}

func TestHealthHandler_SelectionReasonReflectsPoolDecision(t *testing.T) {
	snap := verdict(
		readySnapshot("openai:chat-completions", "expired", "backend-expired"),
		readySnapshot("openai:chat-completions", "degraded", "backend-degraded"),
		readySnapshot("openai:chat-completions", "excluded", "backend-excluded"),
	)

	req := httptest.NewRequest(http.MethodGet, "/registry/health", nil)
	rec := httptest.NewRecorder()
	healthHandler(snap, stubPoolStatusSource{
		status: map[string]poolsnapshot.Status{
			"backend-expired|openai:chat-completions|expired": {
				Configured:          true,
				SnapshotStatus:      "expired",
				SnapshotGeneratedAt: time.Date(2026, 5, 17, 16, 20, 0, 0, time.UTC),
				SnapshotFetchedAt:   time.Date(2026, 5, 17, 16, 19, 0, 0, time.UTC),
			},
			"backend-degraded|openai:chat-completions|degraded": {
				Configured:                   true,
				SnapshotStatus:               "fresh",
				SnapshotGeneratedAt:          time.Date(2026, 5, 17, 16, 20, 0, 0, time.UTC),
				SnapshotFetchedAt:            time.Date(2026, 5, 17, 16, 20, 1, 0, time.UTC),
				EntryFound:                   true,
				EntryState:                   "degraded",
				EntryRoutingReason:           "pool_degraded_stale_sample_window",
				EntryEffectiveSelectionScore: 0.2,
				EntryRecentOutcomeCount:      4,
				EntryRecentWindowStartedAt:   time.Date(2026, 5, 17, 16, 10, 0, 0, time.UTC),
				EntryRecentWindowEndedAt:     time.Date(2026, 5, 17, 16, 14, 0, 0, time.UTC),
				EntryRecentWindowAgeSeconds:  360,
			},
			"backend-excluded|openai:chat-completions|excluded": {
				Configured:                   true,
				SnapshotStatus:               "fresh",
				SnapshotGeneratedAt:          time.Date(2026, 5, 17, 16, 20, 0, 0, time.UTC),
				SnapshotFetchedAt:            time.Date(2026, 5, 17, 16, 20, 1, 0, time.UTC),
				EntryFound:                   true,
				EntryState:                   "excluded",
				EntryExclusionReason:         "pool_cooldown",
				EntryRoutingReason:           "pool_cooldown",
				EntryEffectiveSelectionScore: 0.05,
			},
		},
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want %d", rec.Code, http.StatusOK)
	}

	var out struct {
		Capabilities []struct {
			Status     string `json:"status"`
			Reason     string `json:"reason"`
			OfferingID string `json:"offering_id"`
			Backends   []struct {
				SelectionEligible bool   `json:"selection_eligible"`
				SelectionWeight   int    `json:"selection_weight"`
				SelectionReason   string `json:"selection_reason"`
			} `json:"backends"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	gotByOffering := map[string]struct {
		Status            string
		Reason            string
		SelectionEligible bool
		SelectionWeight   int
		SelectionReason   string
	}{}
	for _, capability := range out.Capabilities {
		if len(capability.Backends) != 1 {
			t.Fatalf("offering %q backend count = %d; want 1", capability.OfferingID, len(capability.Backends))
		}
		gotByOffering[capability.OfferingID] = struct {
			Status            string
			Reason            string
			SelectionEligible bool
			SelectionWeight   int
			SelectionReason   string
		}{
			Status:            capability.Status,
			Reason:            capability.Reason,
			SelectionEligible: capability.Backends[0].SelectionEligible,
			SelectionWeight:   capability.Backends[0].SelectionWeight,
			SelectionReason:   capability.Backends[0].SelectionReason,
		}
	}

	if got := gotByOffering["expired"]; got.Status != "stale" || got.Reason != "pool_snapshot_expired" || got.SelectionEligible || got.SelectionWeight != 0 || got.SelectionReason != "pool_snapshot_expired" {
		t.Fatalf("expired selection = %+v; want status=stale ineligible weight=0 reason=pool_snapshot_expired", got)
	}
	if got := gotByOffering["degraded"]; got.Status != "ready" || got.Reason != "initial_status" || !got.SelectionEligible || got.SelectionWeight != 20 || got.SelectionReason != "pool_degraded_stale_sample_window" {
		t.Fatalf("degraded selection = %+v; want status=ready reason=initial_status eligible weight=20 selection_reason=pool_degraded_stale_sample_window", got)
	}
	if got := gotByOffering["excluded"]; got.Status != "degraded" || got.Reason != "pool_cooldown" || got.SelectionEligible || got.SelectionWeight != 0 || got.SelectionReason != "pool_cooldown" {
		t.Fatalf("excluded selection = %+v; want status=degraded ineligible weight=0 reason=pool_cooldown", got)
	}
}

func TestHealthHandler_OmitsPoolStatusWhenSnapshotUnconfigured(t *testing.T) {
	snap := verdict(readySnapshot("openai:chat-completions", "default", "backend-a"))

	req := httptest.NewRequest(http.MethodGet, "/registry/health", nil)
	rec := httptest.NewRecorder()
	healthHandler(snap, stubPoolStatusSource{}).ServeHTTP(rec, req)

	var out struct {
		Capabilities []struct {
			Backends []struct {
				Pool any `json:"pool"`
			} `json:"backends"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Capabilities) != 1 || len(out.Capabilities[0].Backends) != 1 {
		t.Fatalf("unexpected backend counts: %+v", out.Capabilities)
	}
	if out.Capabilities[0].Backends[0].Pool != nil {
		t.Fatalf("pool = %#v; want omitted/null when unconfigured", out.Capabilities[0].Backends[0].Pool)
	}
}

func TestHealthHandler_EmbedsOfferingLevelPoolAggregate(t *testing.T) {
	snap := verdict(
		readySnapshot("openai:chat-completions", "default", "backend-a"),
		readySnapshot("openai:chat-completions", "default", "backend-b"),
		readySnapshot("openai:chat-completions", "default", "backend-c"),
	)

	req := httptest.NewRequest(http.MethodGet, "/registry/health", nil)
	rec := httptest.NewRecorder()
	healthHandler(snap, stubPoolStatusSource{
		status: map[string]poolsnapshot.Status{
			"backend-a|openai:chat-completions|default": {
				Configured:                            true,
				SnapshotStatus:                        "fresh",
				SnapshotTimeoutSeconds:                0.1,
				SnapshotPollIntervalSeconds:           1,
				SnapshotStaleAfterSeconds:             15,
				SnapshotExpireAfterSeconds:            60,
				SnapshotCooldownDurationSeconds:       300,
				SnapshotCooldownFailureTrigger:        5,
				SnapshotEMAHalfLifeSeconds:            86400,
				SnapshotLatencyTargetMS:               1200,
				SnapshotRecentWindowStaleAfterSeconds: 300,
				SnapshotWindowScoreWeight:             0.7,
				SnapshotEMAScoreWeight:                0.3,
				SnapshotWarmupModifier:                0.25,
				SnapshotWarmupExitSamples:             20,
				EntryFound:                            true,
				EntryState:                            "eligible",
				EntryRoutingReason:                    "pool_eligible",
				EntryEffectiveSelectionScore:          0.9,
				EntrySyntheticConfidence:              0.8,
				EntryRealSuccessScore:                 0.9,
				EntryRealLatencyScore:                 0.8,
				EntryRecentOutcomeCount:               10,
				EntryRecentRoutableOutcomeCount:       9,
				EntryRecentBackendFailureCount:        1,
				EntryRecentWindowStartedAt:            time.Date(2026, 5, 17, 16, 0, 0, 0, time.UTC),
				EntryRecentWindowEndedAt:              time.Date(2026, 5, 17, 16, 4, 0, 0, time.UTC),
				EntryRecentWindowAgeSeconds:           12,
			},
			"backend-b|openai:chat-completions|default": {
				Configured:                            true,
				SnapshotStatus:                        "stale",
				SnapshotTimeoutSeconds:                0.1,
				SnapshotPollIntervalSeconds:           1,
				SnapshotStaleAfterSeconds:             15,
				SnapshotExpireAfterSeconds:            60,
				SnapshotCooldownDurationSeconds:       300,
				SnapshotCooldownFailureTrigger:        5,
				SnapshotEMAHalfLifeSeconds:            86400,
				SnapshotLatencyTargetMS:               1200,
				SnapshotRecentWindowStaleAfterSeconds: 300,
				SnapshotWindowScoreWeight:             0.7,
				SnapshotEMAScoreWeight:                0.3,
				SnapshotWarmupModifier:                0.25,
				SnapshotWarmupExitSamples:             20,
				EntryFound:                            true,
				EntryState:                            "degraded",
				EntryRoutingReason:                    "pool_warmup",
				EntryEffectiveSelectionScore:          0.2,
				EntrySyntheticConfidence:              0.4,
				EntryRealSuccessScore:                 0.3,
				EntryRealLatencyScore:                 0.5,
				EntryAutomaticWarmup:                  true,
				EntryRecentOutcomeCount:               4,
				EntryRecentRoutableOutcomeCount:       4,
				EntryRecentBackendFailureCount:        1,
				EntryRecentWindowStartedAt:            time.Date(2026, 5, 17, 16, 2, 0, 0, time.UTC),
				EntryRecentWindowEndedAt:              time.Date(2026, 5, 17, 16, 5, 0, 0, time.UTC),
				EntryRecentWindowAgeSeconds:           30,
			},
			"backend-c|openai:chat-completions|default": {
				Configured:                            true,
				SnapshotStatus:                        "expired",
				SnapshotTimeoutSeconds:                0.1,
				SnapshotPollIntervalSeconds:           1,
				SnapshotStaleAfterSeconds:             15,
				SnapshotExpireAfterSeconds:            60,
				SnapshotCooldownDurationSeconds:       300,
				SnapshotCooldownFailureTrigger:        5,
				SnapshotEMAHalfLifeSeconds:            86400,
				SnapshotLatencyTargetMS:               1200,
				SnapshotRecentWindowStaleAfterSeconds: 300,
				SnapshotWindowScoreWeight:             0.7,
				SnapshotEMAScoreWeight:                0.3,
				SnapshotWarmupModifier:                0.25,
				SnapshotWarmupExitSamples:             20,
				EntryFound:                            true,
				EntryState:                            "excluded",
				EntryExclusionReason:                  "pool_cooldown",
				EntryRoutingReason:                    "pool_cooldown",
				EntryEffectiveSelectionScore:          0.0,
				EntrySyntheticConfidence:              0.2,
				EntryRealSuccessScore:                 0.1,
				EntryRealLatencyScore:                 0.2,
				EntryRecentOutcomeCount:               7,
				EntryRecentRoutableOutcomeCount:       5,
				EntryRecentBackendFailureCount:        5,
				EntryRecentWindowStartedAt:            time.Date(2026, 5, 17, 16, 1, 0, 0, time.UTC),
				EntryRecentWindowEndedAt:              time.Date(2026, 5, 17, 16, 6, 0, 0, time.UTC),
				EntryRecentWindowAgeSeconds:           90,
			},
		},
	}).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want %d", rec.Code, http.StatusOK)
	}

	var out struct {
		Capabilities []struct {
			Pool *struct {
				Configured                            bool           `json:"configured"`
				SnapshotStatus                        string         `json:"snapshot_status"`
				SnapshotTimeoutSeconds                float64        `json:"snapshot_timeout_seconds"`
				SnapshotPollIntervalSeconds           float64        `json:"snapshot_poll_interval_seconds"`
				SnapshotStaleAfterSeconds             float64        `json:"snapshot_stale_after_seconds"`
				SnapshotExpireAfterSeconds            float64        `json:"snapshot_expire_after_seconds"`
				SnapshotCooldownDurationSeconds       float64        `json:"snapshot_cooldown_duration_seconds"`
				SnapshotCooldownFailureTrigger        int            `json:"snapshot_cooldown_failure_trigger"`
				SnapshotEMAHalfLifeSeconds            float64        `json:"snapshot_ema_half_life_seconds"`
				SnapshotLatencyTargetMS               float64        `json:"snapshot_latency_target_ms"`
				SnapshotRecentWindowStaleAfterSeconds float64        `json:"snapshot_recent_window_stale_after_seconds"`
				SnapshotWindowScoreWeight             float64        `json:"snapshot_window_score_weight"`
				SnapshotEMAScoreWeight                float64        `json:"snapshot_ema_score_weight"`
				SnapshotWarmupModifier                float64        `json:"snapshot_warmup_modifier"`
				SnapshotWarmupExitSamples             int            `json:"snapshot_warmup_exit_samples"`
				BackendCount                          int            `json:"backend_count"`
				EntryFoundCount                       int            `json:"entry_found_count"`
				EligibleCount                         int            `json:"eligible_count"`
				DegradedCount                         int            `json:"degraded_count"`
				ExcludedCount                         int            `json:"excluded_count"`
				AutomaticWarmupCount                  int            `json:"automatic_warmup_count"`
				AverageEffectiveScore                 float64        `json:"average_effective_selection_score"`
				RecentOutcomeCount                    int            `json:"recent_outcome_count"`
				RecentRoutableOutcomeCount            int            `json:"recent_routable_outcome_count"`
				RecentBackendFailureCount             int            `json:"recent_backend_failure_count"`
				RecentWindowStartedAt                 string         `json:"recent_window_started_at"`
				RecentWindowEndedAt                   string         `json:"recent_window_ended_at"`
				AverageRecentWindowAgeSeconds         float64        `json:"average_recent_window_age_seconds"`
				TopRoutingReasons                     map[string]int `json:"top_routing_reasons"`
				TopExclusionReasons                   map[string]int `json:"top_exclusion_reasons"`
			} `json:"pool"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Capabilities) != 1 {
		t.Fatalf("capabilities count = %d; want 1", len(out.Capabilities))
	}
	pool := out.Capabilities[0].Pool
	if pool == nil {
		t.Fatal("aggregate pool = nil; want populated aggregate")
	}
	if !pool.Configured || pool.SnapshotStatus != "expired" {
		t.Fatalf("aggregate configured/status = %+v; want configured expired", pool)
	}
	if pool.SnapshotRecentWindowStaleAfterSeconds != 300 {
		t.Fatalf("aggregate snapshot_recent_window_stale_after_seconds = %v; want 300", pool.SnapshotRecentWindowStaleAfterSeconds)
	}
	if pool.SnapshotTimeoutSeconds != 0.1 || pool.SnapshotPollIntervalSeconds != 1 || pool.SnapshotStaleAfterSeconds != 15 || pool.SnapshotExpireAfterSeconds != 60 {
		t.Fatalf("aggregate broker snapshot policy = %+v; want timeout=0.1 poll=1 stale=15 expire=60", pool)
	}
	if pool.SnapshotCooldownDurationSeconds != 300 || pool.SnapshotCooldownFailureTrigger != 5 {
		t.Fatalf("aggregate cooldown snapshot settings = %+v; want duration=300 trigger=5", pool)
	}
	if pool.SnapshotEMAHalfLifeSeconds != 86400 || pool.SnapshotLatencyTargetMS != 1200 {
		t.Fatalf("aggregate ema/latency snapshot settings = %+v; want ema=86400 latency=1200", pool)
	}
	if pool.SnapshotWindowScoreWeight != 0.7 || pool.SnapshotEMAScoreWeight != 0.3 {
		t.Fatalf("aggregate score weights = %+v; want window=0.7 ema=0.3", pool)
	}
	if pool.SnapshotWarmupModifier != 0.25 || pool.SnapshotWarmupExitSamples != 20 {
		t.Fatalf("aggregate warmup snapshot settings = %+v; want modifier=0.25 exit_samples=20", pool)
	}
	if pool.BackendCount != 3 || pool.EntryFoundCount != 3 {
		t.Fatalf("aggregate backend counts = %+v; want 3/3", pool)
	}
	if pool.EligibleCount != 1 || pool.DegradedCount != 1 || pool.ExcludedCount != 1 {
		t.Fatalf("aggregate state counts = %+v; want eligible=1 degraded=1 excluded=1", pool)
	}
	if pool.AutomaticWarmupCount != 1 {
		t.Fatalf("aggregate automatic_warmup_count = %d; want 1", pool.AutomaticWarmupCount)
	}
	if pool.RecentOutcomeCount != 21 || pool.RecentRoutableOutcomeCount != 18 || pool.RecentBackendFailureCount != 7 {
		t.Fatalf("aggregate recent counts = %+v; want outcomes=21 routable=18 failures=7", pool)
	}
	if pool.RecentWindowStartedAt != "2026-05-17T16:00:00Z" || pool.RecentWindowEndedAt != "2026-05-17T16:06:00Z" {
		t.Fatalf("aggregate recent window bounds = %+v; want started=16:00 ended=16:06", pool)
	}
	if pool.AverageRecentWindowAgeSeconds <= 40 || pool.AverageRecentWindowAgeSeconds >= 50 {
		t.Fatalf("aggregate average_recent_window_age_seconds = %v; want around 44", pool.AverageRecentWindowAgeSeconds)
	}
	if pool.TopRoutingReasons["pool_eligible"] != 1 || pool.TopRoutingReasons["pool_warmup"] != 1 || pool.TopRoutingReasons["pool_cooldown"] != 1 {
		t.Fatalf("aggregate routing reasons = %+v; want eligible=1 warmup=1 cooldown=1", pool.TopRoutingReasons)
	}
	if pool.TopExclusionReasons["pool_cooldown"] != 1 {
		t.Fatalf("aggregate exclusion reasons = %+v; want pool_cooldown=1", pool.TopExclusionReasons)
	}
	if pool.AverageEffectiveScore <= 0.3 || pool.AverageEffectiveScore >= 0.4 {
		t.Fatalf("aggregate average_effective_selection_score = %v; want around 0.366", pool.AverageEffectiveScore)
	}
}
