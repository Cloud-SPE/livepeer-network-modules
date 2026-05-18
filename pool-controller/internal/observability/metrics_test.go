package observability

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestUpdateScoringSettings(t *testing.T) {
	UpdateScoringSettings(config.Scoring{
		CooldownDurationMS:        300000,
		CooldownFailureTrigger:    5,
		EMAHalfLifeMS:             86400000,
		LatencyTargetMS:           1200,
		RecentWindowStaleAfterMS:  300000,
		WindowScoreWeight:         0.7,
		EMAScoreWeight:            0.3,
		WarmupModifier:            0.25,
		WarmupExitSamples:         20,
		TopDegradedLimit:          10,
		TopExcludedLimit:          10,
		WorstOfferingsLimit:       10,
		PublicWorstOfferingsLimit: 5,
	})

	if got := testutil.ToFloat64(scoringSetting.WithLabelValues("cooldown_duration_seconds")); got != 300 {
		t.Fatalf("cooldown_duration_seconds = %v; want 300", got)
	}
	if got := testutil.ToFloat64(scoringSetting.WithLabelValues("ema_score_weight")); got != 0.3 {
		t.Fatalf("ema_score_weight = %v; want 0.3", got)
	}
	if got := testutil.ToFloat64(scoringSetting.WithLabelValues("warmup_exit_samples")); got != 20 {
		t.Fatalf("warmup_exit_samples = %v; want 20", got)
	}
}

func TestUpdateBackendSelectionSnapshot(t *testing.T) {
	cooldownUntil := time.Date(2026, 5, 17, 16, 30, 0, 0, time.UTC)
	UpdateBackendSelectionSnapshot([]types.BackendSelectionState{
		{
			CapabilityID:            "openai:chat-completions",
			OfferingID:              "default",
			State:                   types.BackendSelectionStateEligible,
			RoutingReason:           "pool_eligible",
			EffectiveSelectionScore: 0.8,
			RecentWindowAgeSeconds:  12,
		},
		{
			CapabilityID:            "openai:chat-completions",
			OfferingID:              "default",
			State:                   types.BackendSelectionStateExcluded,
			RoutingReason:           "manual",
			ExclusionReason:         "manual",
			EffectiveSelectionScore: 0.1,
			AutomaticWarmup:         true,
			CooldownUntil:           &cooldownUntil,
			RecentWindowAgeSeconds:  30,
		},
	}, time.Date(2026, 5, 17, 16, 20, 0, 0, time.UTC))

	if got := testutil.ToFloat64(backendSelectionStateTotal.WithLabelValues(
		"openai:chat-completions",
		"default",
		string(types.BackendSelectionStateEligible),
	)); got != 1 {
		t.Fatalf("eligible total = %v; want 1", got)
	}
	if got := testutil.ToFloat64(backendSelectionExclusionReasonTotal.WithLabelValues(
		"openai:chat-completions",
		"default",
		"manual",
	)); got != 1 {
		t.Fatalf("manual exclusion total = %v; want 1", got)
	}
	if got := testutil.ToFloat64(backendSelectionAutomaticWarmupTotal.WithLabelValues(
		"openai:chat-completions",
		"default",
	)); got != 1 {
		t.Fatalf("automatic warmup total = %v; want 1", got)
	}
	if got := testutil.ToFloat64(backendSelectionCooldownTotal.WithLabelValues(
		"openai:chat-completions",
		"default",
	)); got != 1 {
		t.Fatalf("cooldown total = %v; want 1", got)
	}
	if got := testutil.ToFloat64(backendSelectionAverageEffectiveScore.WithLabelValues(
		"openai:chat-completions",
		"default",
	)); got < 0.44 || got > 0.46 {
		t.Fatalf("average effective score = %v; want around 0.45", got)
	}
	if got := testutil.ToFloat64(backendSelectionAverageRecentWindowAgeSeconds.WithLabelValues(
		"openai:chat-completions",
		"default",
	)); got != 21 {
		t.Fatalf("average recent window age = %v; want 21", got)
	}
}

func TestRecordBackendOutcomeIngest(t *testing.T) {
	before := testutil.ToFloat64(backendOutcomeIngestTotal.WithLabelValues(
		"openai:chat-completions",
		"default",
		types.BackendOutcomeBackendFailure,
	))
	RecordBackendOutcomeIngest(types.BackendOutcome{
		CapabilityID: "openai:chat-completions",
		OfferingID:   "default",
		Outcome:      types.BackendOutcomeBackendFailure,
	})
	after := testutil.ToFloat64(backendOutcomeIngestTotal.WithLabelValues(
		"openai:chat-completions",
		"default",
		types.BackendOutcomeBackendFailure,
	))
	if after != before+1 {
		t.Fatalf("backend outcome ingest delta = %v; want 1", after-before)
	}
}

func TestRecordSyntheticProbeRunSummary(t *testing.T) {
	beforeRun := testutil.ToFloat64(syntheticProbeRunsTotal.WithLabelValues("partial"))
	beforeResult := testutil.ToFloat64(syntheticProbeResultsTotal.WithLabelValues(
		"openai:chat-completions",
		"default",
		"succeeded",
		"probe_ok",
	))

	RecordSyntheticProbeRunSummary([]ProbeResultMetric{
		NewProbeResultMetric("openai:chat-completions", "default", "succeeded", "probe_ok"),
		NewProbeResultMetric("openai:embeddings", "default", "skipped", "capability_out_of_scope"),
	}, 2*time.Second, "partial")

	afterRun := testutil.ToFloat64(syntheticProbeRunsTotal.WithLabelValues("partial"))
	if afterRun != beforeRun+1 {
		t.Fatalf("synthetic probe run delta = %v; want 1", afterRun-beforeRun)
	}
	afterResult := testutil.ToFloat64(syntheticProbeResultsTotal.WithLabelValues(
		"openai:chat-completions",
		"default",
		"succeeded",
		"probe_ok",
	))
	if afterResult != beforeResult+1 {
		t.Fatalf("synthetic probe result delta = %v; want 1", afterResult-beforeResult)
	}
}

func TestUpdateAccountingSnapshot(t *testing.T) {
	UpdateAccountingSnapshot(
		[]types.WorkReceipt{
			{ID: "work-1", Status: "stub"},
			{ID: "work-2", Status: "final"},
			{ID: "work-3", Status: "final"},
		},
		[]types.RoundReceipt{
			{ID: "round-1"},
			{ID: "round-2"},
		},
		[]types.PayoutIntent{
			{ID: "payout-1", Status: "pending"},
			{ID: "payout-2", Status: "leased"},
			{ID: "payout-3", Status: "leased"},
		},
	)

	if got := testutil.ToFloat64(workReceiptStatusTotal.WithLabelValues("stub")); got != 1 {
		t.Fatalf("stub work receipt total = %v; want 1", got)
	}
	if got := testutil.ToFloat64(workReceiptStatusTotal.WithLabelValues("final")); got != 2 {
		t.Fatalf("final work receipt total = %v; want 2", got)
	}
	if got := testutil.ToFloat64(roundReceiptTotal); got != 2 {
		t.Fatalf("round receipt total = %v; want 2", got)
	}
	if got := testutil.ToFloat64(payoutIntentStatusTotal.WithLabelValues("leased")); got != 2 {
		t.Fatalf("leased payout intent total = %v; want 2", got)
	}
}

func TestRecordReceiptWriteAndPayoutIntentAction(t *testing.T) {
	receiptBefore := testutil.ToFloat64(receiptWriteTotal.WithLabelValues("work", "final"))
	RecordReceiptWrite("work", "final", 2)
	receiptAfter := testutil.ToFloat64(receiptWriteTotal.WithLabelValues("work", "final"))
	if receiptAfter != receiptBefore+2 {
		t.Fatalf("receipt write delta = %v; want 2", receiptAfter-receiptBefore)
	}

	actionBefore := testutil.ToFloat64(payoutIntentActionTotal.WithLabelValues("claim", "leased"))
	RecordPayoutIntentAction("claim", "leased", 3)
	actionAfter := testutil.ToFloat64(payoutIntentActionTotal.WithLabelValues("claim", "leased"))
	if actionAfter != actionBefore+3 {
		t.Fatalf("payout intent action delta = %v; want 3", actionAfter-actionBefore)
	}
}
