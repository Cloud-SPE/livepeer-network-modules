package poolsnapshot

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
)

func TestStatusForUnconfiguredReturnsEmpty(t *testing.T) {
	cache, err := New(config.PoolSnapshot{}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	status := cache.StatusFor("backend-a", "openai:chat-completions", "default")
	if status.Configured {
		t.Fatalf("Configured = true; want false")
	}
	if status.SnapshotStatus != "" {
		t.Fatalf("SnapshotStatus = %q; want empty", status.SnapshotStatus)
	}
}

func TestPollCachesSnapshotAndReportsFreshness(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"generated_at":"2026-05-17T16:20:00Z",
			"version":1,
			"cooldown_duration_seconds":300,
			"cooldown_failure_trigger":5,
			"ema_half_life_seconds":86400,
			"latency_target_ms":1200,
			"recent_window_stale_after_seconds":300,
			"window_score_weight":0.7,
			"ema_score_weight":0.3,
			"warmup_modifier":0.25,
			"warmup_exit_samples":20,
			"entries":[
				{
					"backend_id":"backend-a",
					"capability_id":"openai:chat-completions",
					"offering_id":"default",
					"state":"eligible",
					"routing_reason":"pool_eligible",
					"effective_selection_score":0.5,
					"warmup_modifier":1.0,
					"max_share_cap":0.5
				}
			]
		}`))
	}))
	defer ts.Close()

	cache, err := New(config.PoolSnapshot{
		URL:            ts.URL,
		TimeoutMS:      100,
		PollIntervalMS: 1000,
		StaleAfterMS:   15000,
		ExpireAfterMS:  60000,
	}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	now := time.Date(2026, 5, 17, 16, 20, 10, 0, time.UTC)
	cache.now = func() time.Time { return now }
	cache.poll(context.Background())

	status := cache.StatusFor("backend-a", "openai:chat-completions", "default")
	if !status.Configured {
		t.Fatal("Configured = false; want true")
	}
	if status.SnapshotStatus != "fresh" {
		t.Fatalf("SnapshotStatus = %q; want fresh", status.SnapshotStatus)
	}
	if !status.EntryFound {
		t.Fatal("EntryFound = false; want true")
	}
	if status.EntryState != "eligible" {
		t.Fatalf("EntryState = %q; want eligible", status.EntryState)
	}
	if status.EntryRoutingReason != "pool_eligible" {
		t.Fatalf("EntryRoutingReason = %q; want pool_eligible", status.EntryRoutingReason)
	}
	if status.SnapshotTimeoutSeconds != 0.1 {
		t.Fatalf("SnapshotTimeoutSeconds = %v; want 0.1", status.SnapshotTimeoutSeconds)
	}
	if status.SnapshotPollIntervalSeconds != 1 {
		t.Fatalf("SnapshotPollIntervalSeconds = %v; want 1", status.SnapshotPollIntervalSeconds)
	}
	if status.SnapshotStaleAfterSeconds != 15 {
		t.Fatalf("SnapshotStaleAfterSeconds = %v; want 15", status.SnapshotStaleAfterSeconds)
	}
	if status.SnapshotExpireAfterSeconds != 60 {
		t.Fatalf("SnapshotExpireAfterSeconds = %v; want 60", status.SnapshotExpireAfterSeconds)
	}
	if status.SnapshotCooldownDurationSeconds != 300 {
		t.Fatalf("SnapshotCooldownDurationSeconds = %v; want 300", status.SnapshotCooldownDurationSeconds)
	}
	if status.SnapshotCooldownFailureTrigger != 5 {
		t.Fatalf("SnapshotCooldownFailureTrigger = %d; want 5", status.SnapshotCooldownFailureTrigger)
	}
	if status.SnapshotEMAHalfLifeSeconds != 86400 {
		t.Fatalf("SnapshotEMAHalfLifeSeconds = %v; want 86400", status.SnapshotEMAHalfLifeSeconds)
	}
	if status.SnapshotLatencyTargetMS != 1200 {
		t.Fatalf("SnapshotLatencyTargetMS = %v; want 1200", status.SnapshotLatencyTargetMS)
	}
	if status.SnapshotRecentWindowStaleAfterSeconds != 300 {
		t.Fatalf("SnapshotRecentWindowStaleAfterSeconds = %v; want 300", status.SnapshotRecentWindowStaleAfterSeconds)
	}
	if status.SnapshotWindowScoreWeight != 0.7 || status.SnapshotEMAScoreWeight != 0.3 {
		t.Fatalf("snapshot score weights = %+v; want window=0.7 ema=0.3", status)
	}
	if status.SnapshotWarmupModifier != 0.25 || status.SnapshotWarmupExitSamples != 20 {
		t.Fatalf("snapshot warmup settings = %+v; want modifier=0.25 exit_samples=20", status)
	}
	if status.EntryEffectiveSelectionScore != 0.5 {
		t.Fatalf("EntryEffectiveSelectionScore = %v; want 0.5", status.EntryEffectiveSelectionScore)
	}
	if status.SnapshotAgeSeconds != 10 {
		t.Fatalf("SnapshotAgeSeconds = %v; want 10", status.SnapshotAgeSeconds)
	}
}

func TestStatusForReportsFetchErrorBeforeBootstrap(t *testing.T) {
	cache, err := New(config.PoolSnapshot{
		URL:            "http://pool-controller:8080",
		TimeoutMS:      100,
		PollIntervalMS: 1000,
		StaleAfterMS:   15000,
		ExpireAfterMS:  60000,
	}, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	cache.recordError(context.DeadlineExceeded)
	status := cache.StatusFor("backend-a", "openai:chat-completions", "default")
	if status.SnapshotStatus != "fetch_error" {
		t.Fatalf("SnapshotStatus = %q; want fetch_error", status.SnapshotStatus)
	}
	if status.LastError == "" {
		t.Fatal("LastError empty; want populated fetch error")
	}
}
