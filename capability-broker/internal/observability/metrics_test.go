package observability

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecordBackendSelection(t *testing.T) {
	before := testutil.ToFloat64(backendSelectionsTotal.WithLabelValues(
		"openai:chat-completions",
		"shared",
		"member-west",
	))

	RecordBackendSelection("openai:chat-completions", "shared", "member-west")

	after := testutil.ToFloat64(backendSelectionsTotal.WithLabelValues(
		"openai:chat-completions",
		"shared",
		"member-west",
	))
	if after != before+1 {
		t.Fatalf("counter delta = %v; want %v", after-before, 1.0)
	}
}

func TestRecordBackendSelectionFinal(t *testing.T) {
	before := testutil.ToFloat64(backendSelectionFinalTotal.WithLabelValues(
		"openai:chat-completions",
		"shared",
		"member-west",
		"pool_warmup",
	))

	RecordBackendSelectionFinal("openai:chat-completions", "shared", "member-west", "pool_warmup")

	after := testutil.ToFloat64(backendSelectionFinalTotal.WithLabelValues(
		"openai:chat-completions",
		"shared",
		"member-west",
		"pool_warmup",
	))
	if after != before+1 {
		t.Fatalf("counter delta = %v; want %v", after-before, 1.0)
	}
}

func TestRecordBackendSelectionDeniedAndExhausted(t *testing.T) {
	deniedBefore := testutil.ToFloat64(backendSelectionDeniedTotal.WithLabelValues(
		"openai:chat-completions",
		"shared",
		"member-west",
		"pool_excluded",
	))
	RecordBackendSelectionDenied("openai:chat-completions", "shared", "member-west", "pool_excluded")
	deniedAfter := testutil.ToFloat64(backendSelectionDeniedTotal.WithLabelValues(
		"openai:chat-completions",
		"shared",
		"member-west",
		"pool_excluded",
	))
	if deniedAfter != deniedBefore+1 {
		t.Fatalf("denied counter delta = %v; want %v", deniedAfter-deniedBefore, 1.0)
	}

	exhaustedBefore := testutil.ToFloat64(backendSelectionExhaustedTotal.WithLabelValues(
		"openai:chat-completions",
		"shared",
		"mixed_denial_reasons",
	))
	RecordBackendSelectionExhausted("openai:chat-completions", "shared", "mixed_denial_reasons")
	exhaustedAfter := testutil.ToFloat64(backendSelectionExhaustedTotal.WithLabelValues(
		"openai:chat-completions",
		"shared",
		"mixed_denial_reasons",
	))
	if exhaustedAfter != exhaustedBefore+1 {
		t.Fatalf("exhausted counter delta = %v; want %v", exhaustedAfter-exhaustedBefore, 1.0)
	}
}

func TestRecordBackendOutcomeEmitAndWorkReceiptEmit(t *testing.T) {
	outcomeBefore := testutil.ToFloat64(backendOutcomeEmitTotal.WithLabelValues("success", "success"))
	RecordBackendOutcomeEmit("success", "success")
	outcomeAfter := testutil.ToFloat64(backendOutcomeEmitTotal.WithLabelValues("success", "success"))
	if outcomeAfter != outcomeBefore+1 {
		t.Fatalf("backend outcome emit delta = %v; want 1", outcomeAfter-outcomeBefore)
	}

	receiptBefore := testutil.ToFloat64(workReceiptEmitTotal.WithLabelValues("final", "error"))
	RecordWorkReceiptEmit("final", "error")
	receiptAfter := testutil.ToFloat64(workReceiptEmitTotal.WithLabelValues("final", "error"))
	if receiptAfter != receiptBefore+1 {
		t.Fatalf("work receipt emit delta = %v; want 1", receiptAfter-receiptBefore)
	}
}

func TestUpdatePoolSnapshotMetrics(t *testing.T) {
	now := time.Date(2026, 5, 17, 16, 20, 0, 0, time.UTC)
	UpdatePoolSnapshotMetrics(
		"fresh",
		now.Add(-10*time.Second),
		now,
		1500*time.Millisecond,
		5*time.Second,
		15*time.Second,
		60*time.Second,
		[]PoolSnapshotEntryMetric{
			{
				CapabilityID:           "openai:chat-completions",
				OfferingID:             "default",
				State:                  "eligible",
				RoutingReason:          "pool_eligible",
				RecentWindowAgeSeconds: 12,
			},
			{
				CapabilityID:           "openai:chat-completions",
				OfferingID:             "default",
				State:                  "degraded",
				RoutingReason:          "pool_warmup",
				AutomaticWarmup:        true,
				CooldownUntil:          now.Add(5 * time.Minute),
				RecentWindowAgeSeconds: 30,
			},
		},
		now,
	)

	if got := testutil.ToFloat64(poolSnapshotCacheStatus.WithLabelValues("fresh")); got != 1 {
		t.Fatalf("pool snapshot fresh status = %v; want 1", got)
	}
	if got := testutil.ToFloat64(poolSnapshotSettingSeconds.WithLabelValues("poll_interval")); got != 5 {
		t.Fatalf("poll interval setting = %v; want 5", got)
	}
	if got := testutil.ToFloat64(poolSnapshotEntryStateTotal.WithLabelValues(
		"openai:chat-completions", "default", "eligible",
	)); got != 1 {
		t.Fatalf("eligible entry total = %v; want 1", got)
	}
	if got := testutil.ToFloat64(poolSnapshotRoutingReasonTotal.WithLabelValues(
		"openai:chat-completions", "default", "pool_warmup",
	)); got != 1 {
		t.Fatalf("warmup routing reason total = %v; want 1", got)
	}
	if got := testutil.ToFloat64(poolSnapshotAutomaticWarmupTotal.WithLabelValues(
		"openai:chat-completions", "default",
	)); got != 1 {
		t.Fatalf("automatic warmup total = %v; want 1", got)
	}
	if got := testutil.ToFloat64(poolSnapshotCooldownTotal.WithLabelValues(
		"openai:chat-completions", "default",
	)); got != 1 {
		t.Fatalf("cooldown total = %v; want 1", got)
	}
	if got := testutil.ToFloat64(poolSnapshotAverageRecentWindowAge.WithLabelValues(
		"openai:chat-completions", "default",
	)); got != 21 {
		t.Fatalf("average recent window age = %v; want 21", got)
	}
}
