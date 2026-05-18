package repo

import (
	"math"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestStateRepoSaveAndListSnapshots(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	first := Snapshot{
		ID:            "2026-05-16T12:00:00Z",
		CreatedAt:     time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC),
		Source:        "startup",
		MemberCount:   1,
		RenderedBytes: 10,
	}
	second := Snapshot{
		ID:            "2026-05-16T13:00:00Z",
		CreatedAt:     time.Date(2026, 5, 16, 13, 0, 0, 0, time.UTC),
		Source:        "reload",
		MemberCount:   2,
		RenderedBytes: 20,
	}
	if err := repo.SaveSnapshot(first); err != nil {
		t.Fatalf("SaveSnapshot(first) error = %v", err)
	}
	if err := repo.SaveSnapshot(second); err != nil {
		t.Fatalf("SaveSnapshot(second) error = %v", err)
	}

	latest, err := repo.LatestSnapshot()
	if err != nil {
		t.Fatalf("LatestSnapshot() error = %v", err)
	}
	if latest == nil || latest.ID != second.ID {
		t.Fatalf("LatestSnapshot() = %#v, want %q", latest, second.ID)
	}

	items, err := repo.ListSnapshots(10)
	if err != nil {
		t.Fatalf("ListSnapshots() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(ListSnapshots()) = %d, want 2", len(items))
	}
	if items[0].ID != second.ID || items[1].ID != first.ID {
		t.Fatalf("snapshot order = %#v", items)
	}
}

func TestStateRepoSyncBackendSelectionStates(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	cfg := &config.Config{
		Members: []config.Member{
			{
				EthAddress: "0xabc",
				Backends: []config.Backend{
					{
						ID:        "backend-a",
						Transport: "http",
						Offerings: []config.Offering{
							{CapabilityID: "openai:chat-completions", OfferingID: "default"},
							{CapabilityID: "openai:embeddings", OfferingID: "default"},
						},
					},
				},
			},
		},
	}

	if err := repo.SyncBackendSelectionStates(cfg); err != nil {
		t.Fatalf("SyncBackendSelectionStates() error = %v", err)
	}

	items, err := repo.ListBackendSelectionStates()
	if err != nil {
		t.Fatalf("ListBackendSelectionStates() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(ListBackendSelectionStates()) = %d, want 2", len(items))
	}
	if items[0].State != types.BackendSelectionStateEligible {
		t.Fatalf("items[0].State = %q, want %q", items[0].State, types.BackendSelectionStateEligible)
	}
	if items[0].SyntheticConfidence != 0.5 || items[0].RealSuccessScore != 0.5 || items[0].RealLatencyScore != 0.5 {
		t.Fatalf("default neutral scores = %#v", items[0])
	}
	if items[0].WarmupModifier != 1.0 {
		t.Fatalf("items[0].WarmupModifier = %v, want 1.0", items[0].WarmupModifier)
	}
	if items[0].EffectiveSelectionScore != 0.5 {
		t.Fatalf("items[0].EffectiveSelectionScore = %v, want 0.5", items[0].EffectiveSelectionScore)
	}
	if items[0].CreatedAt.IsZero() || items[0].UpdatedAt.IsZero() {
		t.Fatalf("timestamps not initialized: %#v", items[0])
	}

	override := items[0]
	override.State = types.BackendSelectionStateDegraded
	override.SyntheticConfidence = 0.25
	override.RealSuccessScore = 0.25
	override.RealLatencyScore = 0.25
	if err := repo.SaveBackendSelectionState(override); err != nil {
		t.Fatalf("SaveBackendSelectionState() error = %v", err)
	}
	if err := repo.SyncBackendSelectionStates(cfg); err != nil {
		t.Fatalf("SyncBackendSelectionStates(second) error = %v", err)
	}

	items, err = repo.ListBackendSelectionStates()
	if err != nil {
		t.Fatalf("ListBackendSelectionStates(second) error = %v", err)
	}
	if items[0].State != types.BackendSelectionStateDegraded {
		t.Fatalf("items[0].State after resync = %q, want %q", items[0].State, types.BackendSelectionStateDegraded)
	}
	if math.Abs(items[0].EffectiveSelectionScore-0.25) > 1e-9 {
		t.Fatalf("items[0].EffectiveSelectionScore after resync = %v, want 0.25", items[0].EffectiveSelectionScore)
	}
}

func TestStateRepoSaveAndListReceipts(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	work := types.WorkReceipt{
		ID:                "work-1",
		CreatedAt:         time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC),
		RequestID:         "req-1",
		CapabilityID:      "openai:chat-completions",
		OfferingID:        "shared",
		MemberEthAddress:  "0xabc",
		BackendID:         "backend-a",
		ActualUnits:       42,
		GatewayRevenueWei: "1000",
		Status:            "final",
	}
	round := types.RoundReceipt{
		ID:               "round-1",
		CreatedAt:        time.Date(2026, 5, 16, 13, 0, 0, 0, time.UTC),
		RoundID:          "123",
		PoolRevenueWei:   "10000",
		PoolCutWei:       "1000",
		DistributableWei: "9000",
	}

	if err := repo.SaveWorkReceipt(work); err != nil {
		t.Fatalf("SaveWorkReceipt() error = %v", err)
	}
	if err := repo.SaveRoundReceipt(round); err != nil {
		t.Fatalf("SaveRoundReceipt() error = %v", err)
	}

	workItems, err := repo.ListWorkReceipts(10)
	if err != nil {
		t.Fatalf("ListWorkReceipts() error = %v", err)
	}
	if len(workItems) != 1 || workItems[0].ID != "work-1" {
		t.Fatalf("work receipts = %#v", workItems)
	}

	roundItems, err := repo.ListRoundReceipts(10)
	if err != nil {
		t.Fatalf("ListRoundReceipts() error = %v", err)
	}
	if len(roundItems) != 1 || roundItems[0].ID != "round-1" {
		t.Fatalf("round receipts = %#v", roundItems)
	}

	gotWork, err := repo.GetWorkReceipts([]string{"work-1"})
	if err != nil {
		t.Fatalf("GetWorkReceipts() error = %v", err)
	}
	if len(gotWork) != 1 || gotWork[0].ID != "work-1" {
		t.Fatalf("GetWorkReceipts() = %#v", gotWork)
	}
}

func TestStateRepoApplyBackendOutcome(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	cfg := &config.Config{
		Members: []config.Member{
			{
				EthAddress: "0xabc",
				Backends: []config.Backend{
					{
						ID:        "backend-a",
						Transport: "http",
						Offerings: []config.Offering{
							{CapabilityID: "openai:chat-completions", OfferingID: "default"},
						},
					},
				},
			},
		},
	}
	if err := repo.SyncBackendSelectionStates(cfg); err != nil {
		t.Fatalf("SyncBackendSelectionStates() error = %v", err)
	}

	occurredAt := time.Date(2026, 5, 17, 16, 30, 0, 0, time.UTC)
	updated, err := repo.ApplyBackendOutcome(types.BackendOutcome{
		MemberEthAddress: "0xabc",
		BackendID:        "backend-a",
		CapabilityID:     "openai:chat-completions",
		OfferingID:       "default",
		Outcome:          types.BackendOutcomeSuccess,
		LatencyMetricMS:  600,
		OccurredAt:       &occurredAt,
	})
	if err != nil {
		t.Fatalf("ApplyBackendOutcome(success) error = %v", err)
	}
	if updated.LastRealOutcomeAt == nil || !updated.LastRealOutcomeAt.Equal(occurredAt) {
		t.Fatalf("LastRealOutcomeAt = %#v, want %v", updated.LastRealOutcomeAt, occurredAt)
	}
	if math.Abs(updated.RealSuccessScore-1.0) > 1e-9 {
		t.Fatalf("RealSuccessScore after first success = %v, want 1.0", updated.RealSuccessScore)
	}
	if math.Abs(updated.RealLatencyScore-1.0) > 1e-9 {
		t.Fatalf("RealLatencyScore after first latency update = %v, want 1.0", updated.RealLatencyScore)
	}
	if updated.EffectiveSelectionScore <= 0.5 {
		t.Fatalf("EffectiveSelectionScore after success = %v, want > 0.5", updated.EffectiveSelectionScore)
	}

	updated, err = repo.ApplyBackendOutcome(types.BackendOutcome{
		MemberEthAddress: "0xabc",
		BackendID:        "backend-a",
		CapabilityID:     "openai:chat-completions",
		OfferingID:       "default",
		Outcome:          types.BackendOutcomeBackendFailure,
		OccurredAt:       ptrTime(occurredAt.Add(1 * time.Minute)),
	})
	if err != nil {
		t.Fatalf("ApplyBackendOutcome(backend_failure) error = %v", err)
	}
	if updated.RealSuccessScore >= 1.0 || updated.RealSuccessScore <= 0.6 {
		t.Fatalf("RealSuccessScore after backend failure = %v, want in (0.6, 1.0)", updated.RealSuccessScore)
	}

	updated, err = repo.ApplyBackendOutcome(types.BackendOutcome{
		MemberEthAddress: "0xabc",
		BackendID:        "backend-a",
		CapabilityID:     "openai:chat-completions",
		OfferingID:       "default",
		Outcome:          types.BackendOutcomeCallerFailure,
		LatencyMetricMS:  9000,
		OccurredAt:       ptrTime(occurredAt.Add(2 * time.Minute)),
	})
	if err != nil {
		t.Fatalf("ApplyBackendOutcome(caller_failure) error = %v", err)
	}
	if updated.RealSuccessScore >= 1.0 || updated.RealSuccessScore <= 0.6 {
		t.Fatalf("RealSuccessScore after caller failure = %v, want still in (0.6, 1.0)", updated.RealSuccessScore)
	}
	if updated.RealLatencyScore >= 0.5 {
		t.Fatalf("RealLatencyScore after slow latency signal = %v, want reduced below 0.5", updated.RealLatencyScore)
	}
}

func TestStateRepoApplySyntheticProbeObservation(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	cfg := &config.Config{
		Members: []config.Member{
			{
				EthAddress: "0xabc",
				Backends: []config.Backend{
					{
						ID:        "backend-a",
						Transport: "http",
						Offerings: []config.Offering{
							{CapabilityID: "openai:chat-completions", OfferingID: "default"},
						},
					},
				},
			},
		},
	}
	if err := repo.SyncBackendSelectionStates(cfg); err != nil {
		t.Fatalf("SyncBackendSelectionStates() error = %v", err)
	}

	at := time.Date(2026, 5, 17, 17, 0, 0, 0, time.UTC)
	updated, err := repo.ApplySyntheticProbeObservation(types.SyntheticProbeObservation{
		MemberEthAddress: "0xabc",
		BackendID:        "backend-a",
		CapabilityID:     "openai:chat-completions",
		OfferingID:       "default",
		Success:          true,
		Result:           "probe_ok",
		ObservedAt:       at,
	})
	if err != nil {
		t.Fatalf("ApplySyntheticProbeObservation(success) error = %v", err)
	}
	if updated.LastSyntheticAt == nil || !updated.LastSyntheticAt.Equal(at) {
		t.Fatalf("LastSyntheticAt = %#v, want %v", updated.LastSyntheticAt, at)
	}
	if updated.LastSyntheticResult != "probe_ok" {
		t.Fatalf("LastSyntheticResult = %q, want probe_ok", updated.LastSyntheticResult)
	}
	if updated.ConsecutiveSyntheticFailures != 0 {
		t.Fatalf("ConsecutiveSyntheticFailures = %d, want 0", updated.ConsecutiveSyntheticFailures)
	}
	if updated.SyntheticConfidence <= 0.5 {
		t.Fatalf("SyntheticConfidence after success = %v, want > 0.5", updated.SyntheticConfidence)
	}
	if updated.WarmupModifier != 0.25 {
		t.Fatalf("WarmupModifier after first synthetic success = %v, want 0.25", updated.WarmupModifier)
	}
	if !updated.AutomaticWarmup || updated.WarmupSource != "automatic" {
		t.Fatalf("warmup flags after first synthetic success = automatic:%v source:%q, want automatic", updated.AutomaticWarmup, updated.WarmupSource)
	}
	if updated.RoutingReason != "pool_warmup" {
		t.Fatalf("RoutingReason after first synthetic success = %q, want pool_warmup", updated.RoutingReason)
	}

	for i := 0; i < 3; i++ {
		updated, err = repo.ApplySyntheticProbeObservation(types.SyntheticProbeObservation{
			MemberEthAddress: "0xabc",
			BackendID:        "backend-a",
			CapabilityID:     "openai:chat-completions",
			OfferingID:       "default",
			Success:          false,
			Result:           "probe_failed",
			ObservedAt:       at.Add(time.Duration(i+1) * time.Minute),
		})
		if err != nil {
			t.Fatalf("ApplySyntheticProbeObservation(failure %d) error = %v", i+1, err)
		}
	}
	if updated.ConsecutiveSyntheticFailures != 3 {
		t.Fatalf("ConsecutiveSyntheticFailures = %d, want 3", updated.ConsecutiveSyntheticFailures)
	}
	if updated.State != types.BackendSelectionStateExcluded {
		t.Fatalf("State after threshold = %q, want excluded", updated.State)
	}
	if updated.ExclusionReason != "synthetic_probe_failure_threshold" {
		t.Fatalf("ExclusionReason = %q, want synthetic_probe_failure_threshold", updated.ExclusionReason)
	}
	if updated.RoutingReason != "synthetic_probe_failure_threshold" {
		t.Fatalf("RoutingReason after threshold = %q, want synthetic_probe_failure_threshold", updated.RoutingReason)
	}

	updated, err = repo.ApplySyntheticProbeObservation(types.SyntheticProbeObservation{
		MemberEthAddress: "0xabc",
		BackendID:        "backend-a",
		CapabilityID:     "openai:chat-completions",
		OfferingID:       "default",
		Success:          true,
		Result:           "probe_ok",
		ObservedAt:       at.Add(10 * time.Minute),
	})
	if err != nil {
		t.Fatalf("ApplySyntheticProbeObservation(recovery) error = %v", err)
	}
	if updated.State != types.BackendSelectionStateDegraded {
		t.Fatalf("State after recovery = %q, want degraded warm-up reentry", updated.State)
	}
	if updated.ExclusionReason != "" {
		t.Fatalf("ExclusionReason after recovery = %q, want empty", updated.ExclusionReason)
	}
	if updated.WarmupModifier != 0.25 {
		t.Fatalf("WarmupModifier after recovery = %v, want 0.25", updated.WarmupModifier)
	}
	if !updated.AutomaticWarmup || updated.WarmupSource != "automatic" {
		t.Fatalf("warmup flags after recovery = automatic:%v source:%q, want automatic", updated.AutomaticWarmup, updated.WarmupSource)
	}
	if updated.RoutingReason != "pool_warmup" {
		t.Fatalf("RoutingReason after recovery = %q, want pool_warmup", updated.RoutingReason)
	}
}

func TestStateRepoApplyBackendOutcomeTransitionsByEffectiveScore(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	cfg := &config.Config{
		Members: []config.Member{
			{
				EthAddress: "0xabc",
				Backends: []config.Backend{
					{
						ID:        "backend-a",
						Transport: "http",
						Offerings: []config.Offering{
							{CapabilityID: "openai:chat-completions", OfferingID: "default"},
						},
					},
				},
			},
		},
	}
	if err := repo.SyncBackendSelectionStates(cfg); err != nil {
		t.Fatalf("SyncBackendSelectionStates() error = %v", err)
	}

	degraded, err := repo.GetBackendSelectionState("0xabc", "backend-a", "openai:chat-completions", "default")
	if err != nil {
		t.Fatalf("GetBackendSelectionState(degraded) error = %v", err)
	}
	degraded.SyntheticConfidence = 0.20
	degraded.RealSuccessScore = 0.20
	degraded.RealLatencyScore = 0.20
	degraded.WarmupModifier = 1.0
	if err := repo.SaveBackendSelectionState(degraded); err != nil {
		t.Fatalf("SaveBackendSelectionState(degraded) error = %v", err)
	}
	degraded, err = repo.GetBackendSelectionState("0xabc", "backend-a", "openai:chat-completions", "default")
	if err != nil {
		t.Fatalf("GetBackendSelectionState(saved degraded) error = %v", err)
	}

	if degraded.State != types.BackendSelectionStateDegraded {
		t.Fatalf("degraded state after save = %q, want %q", degraded.State, types.BackendSelectionStateDegraded)
	}

	updated, err := repo.ApplyBackendOutcome(types.BackendOutcome{
		MemberEthAddress: "0xabc",
		BackendID:        "backend-a",
		CapabilityID:     "openai:chat-completions",
		OfferingID:       "default",
		Outcome:          types.BackendOutcomeCallerFailure,
		OccurredAt:       ptrTime(time.Date(2026, 5, 17, 16, 40, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("ApplyBackendOutcome(degraded) error = %v", err)
	}
	if updated.State != types.BackendSelectionStateEligible {
		t.Fatalf("state after neutral caller failure = %q, want %q", updated.State, types.BackendSelectionStateEligible)
	}
	if updated.ExclusionReason != "" {
		t.Fatalf("eligible exclusion reason = %q, want empty", updated.ExclusionReason)
	}

	excluded := updated
	excluded.SyntheticConfidence = 0.05
	excluded.RealSuccessScore = 0.05
	excluded.RealLatencyScore = 0.05
	if err := repo.SaveBackendSelectionState(excluded); err != nil {
		t.Fatalf("SaveBackendSelectionState(excluded) error = %v", err)
	}
	excluded, err = repo.GetBackendSelectionState("0xabc", "backend-a", "openai:chat-completions", "default")
	if err != nil {
		t.Fatalf("GetBackendSelectionState(excluded) error = %v", err)
	}
	if excluded.State != types.BackendSelectionStateExcluded {
		t.Fatalf("excluded state after save = %q, want %q", excluded.State, types.BackendSelectionStateExcluded)
	}

	updated, err = repo.ApplyBackendOutcome(types.BackendOutcome{
		MemberEthAddress: "0xabc",
		BackendID:        "backend-a",
		CapabilityID:     "openai:chat-completions",
		OfferingID:       "default",
		Outcome:          types.BackendOutcomeCallerFailure,
		OccurredAt:       ptrTime(time.Date(2026, 5, 17, 16, 41, 0, 0, time.UTC)),
	})
	if err != nil {
		t.Fatalf("ApplyBackendOutcome(excluded) error = %v", err)
	}
	if updated.State != types.BackendSelectionStateEligible {
		t.Fatalf("excluded state after neutral caller failure = %q, want eligible from recalculated scores", updated.State)
	}
}

func TestStateRepoApplyBackendOutcomeDriftsEMAAfterInactivity(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	cfg := &config.Config{
		Members: []config.Member{{
			EthAddress: "0xabc",
			Backends: []config.Backend{{
				ID:        "backend-a",
				Transport: "http",
				Offerings: []config.Offering{{CapabilityID: "openai:chat-completions", OfferingID: "default"}},
			}},
		}},
	}
	if err := repo.SyncBackendSelectionStates(cfg); err != nil {
		t.Fatalf("SyncBackendSelectionStates() error = %v", err)
	}

	start := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)
	updated, err := repo.ApplyBackendOutcome(types.BackendOutcome{
		MemberEthAddress: "0xabc",
		BackendID:        "backend-a",
		CapabilityID:     "openai:chat-completions",
		OfferingID:       "default",
		Outcome:          types.BackendOutcomeSuccess,
		LatencyMetricMS:  500,
		OccurredAt:       &start,
	})
	if err != nil {
		t.Fatalf("ApplyBackendOutcome(initial success) error = %v", err)
	}
	if updated.RealSuccessScore < 0.99 {
		t.Fatalf("initial RealSuccessScore = %v, want near 1", updated.RealSuccessScore)
	}

	later := start.Add(48 * time.Hour)
	updated, err = repo.ApplyBackendOutcome(types.BackendOutcome{
		MemberEthAddress: "0xabc",
		BackendID:        "backend-a",
		CapabilityID:     "openai:chat-completions",
		OfferingID:       "default",
		Outcome:          types.BackendOutcomeCallerFailure,
		OccurredAt:       &later,
	})
	if err != nil {
		t.Fatalf("ApplyBackendOutcome(after inactivity) error = %v", err)
	}
	if updated.RealSuccessScore >= 0.75 || updated.RealSuccessScore <= 0.45 {
		t.Fatalf("RealSuccessScore after inactivity drift = %v, want near neutral", updated.RealSuccessScore)
	}
}

func TestStateRepoApplyBackendOutcomeExitsWarmupAfterEnoughSamples(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	cfg := &config.Config{
		Members: []config.Member{{
			EthAddress: "0xabc",
			Backends: []config.Backend{{
				ID:        "backend-a",
				Transport: "http",
				Offerings: []config.Offering{{CapabilityID: "openai:chat-completions", OfferingID: "default"}},
			}},
		}},
	}
	if err := repo.SyncBackendSelectionStates(cfg); err != nil {
		t.Fatalf("SyncBackendSelectionStates() error = %v", err)
	}

	item, err := repo.GetBackendSelectionState("0xabc", "backend-a", "openai:chat-completions", "default")
	if err != nil {
		t.Fatalf("GetBackendSelectionState() error = %v", err)
	}
	item.WarmupModifier = 0.25
	if err := repo.SaveBackendSelectionState(item); err != nil {
		t.Fatalf("SaveBackendSelectionState() error = %v", err)
	}

	base := time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC)
	var updated types.BackendSelectionState
	for i := 0; i < 20; i++ {
		updated, err = repo.ApplyBackendOutcome(types.BackendOutcome{
			MemberEthAddress: "0xabc",
			BackendID:        "backend-a",
			CapabilityID:     "openai:chat-completions",
			OfferingID:       "default",
			Outcome:          types.BackendOutcomeSuccess,
			LatencyMetricMS:  500,
			OccurredAt:       ptrTime(base.Add(time.Duration(i) * 10 * time.Second)),
		})
		if err != nil {
			t.Fatalf("ApplyBackendOutcome(sample %d) error = %v", i, err)
		}
	}
	if updated.WarmupModifier != 1.0 {
		t.Fatalf("WarmupModifier after enough samples = %v, want 1.0", updated.WarmupModifier)
	}
}

func TestStateRepoApplyBackendOutcomePreservesManualQuarantine(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	cfg := &config.Config{
		Members: []config.Member{
			{
				EthAddress: "0xabc",
				Backends: []config.Backend{
					{
						ID:        "backend-a",
						Transport: "http",
						Offerings: []config.Offering{
							{CapabilityID: "openai:chat-completions", OfferingID: "default"},
						},
					},
				},
			},
		},
	}
	if err := repo.SyncBackendSelectionStates(cfg); err != nil {
		t.Fatalf("SyncBackendSelectionStates() error = %v", err)
	}

	item, err := repo.GetBackendSelectionState("0xabc", "backend-a", "openai:chat-completions", "default")
	if err != nil {
		t.Fatalf("GetBackendSelectionState() error = %v", err)
	}
	item.State = types.BackendSelectionStateQuarantined
	item.ExclusionReason = "manual"
	if err := repo.SaveBackendSelectionState(item); err != nil {
		t.Fatalf("SaveBackendSelectionState(quarantine) error = %v", err)
	}

	updated, err := repo.ApplyBackendOutcome(types.BackendOutcome{
		MemberEthAddress: "0xabc",
		BackendID:        "backend-a",
		CapabilityID:     "openai:chat-completions",
		OfferingID:       "default",
		Outcome:          types.BackendOutcomeSuccess,
		LatencyMetricMS:  500,
	})
	if err != nil {
		t.Fatalf("ApplyBackendOutcome(quarantine) error = %v", err)
	}
	if updated.State != types.BackendSelectionStateQuarantined {
		t.Fatalf("quarantined state after outcome = %q, want %q", updated.State, types.BackendSelectionStateQuarantined)
	}
	if updated.ExclusionReason != "manual" {
		t.Fatalf("quarantined reason after outcome = %q, want manual", updated.ExclusionReason)
	}
}

func TestStateRepoApplyBackendOutcomeTransitionsStateBands(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	cfg := &config.Config{
		Members: []config.Member{
			{
				EthAddress: "0xabc",
				Backends: []config.Backend{
					{
						ID:        "backend-a",
						Transport: "http",
						Offerings: []config.Offering{
							{CapabilityID: "openai:chat-completions", OfferingID: "default"},
						},
					},
				},
			},
		},
	}
	if err := repo.SyncBackendSelectionStates(cfg); err != nil {
		t.Fatalf("SyncBackendSelectionStates() error = %v", err)
	}

	item, err := repo.GetBackendSelectionState("0xabc", "backend-a", "openai:chat-completions", "default")
	if err != nil {
		t.Fatalf("GetBackendSelectionState() error = %v", err)
	}
	item.SyntheticConfidence = 0.05
	item.RealSuccessScore = 0.05
	item.RealLatencyScore = 0.05
	if err := repo.SaveBackendSelectionState(item); err != nil {
		t.Fatalf("SaveBackendSelectionState(low-score) error = %v", err)
	}
	item, err = repo.GetBackendSelectionState("0xabc", "backend-a", "openai:chat-completions", "default")
	if err != nil {
		t.Fatalf("GetBackendSelectionState(low-score) error = %v", err)
	}
	if item.State != types.BackendSelectionStateExcluded {
		t.Fatalf("state with score below floor = %q, want excluded", item.State)
	}
	if item.ExclusionReason != "score_below_floor" {
		t.Fatalf("exclusion reason = %q, want score_below_floor", item.ExclusionReason)
	}

	item.SyntheticConfidence = 0.2
	item.RealSuccessScore = 0.2
	item.RealLatencyScore = 0.2
	if err := repo.SaveBackendSelectionState(item); err != nil {
		t.Fatalf("SaveBackendSelectionState(degraded) error = %v", err)
	}
	item, err = repo.GetBackendSelectionState("0xabc", "backend-a", "openai:chat-completions", "default")
	if err != nil {
		t.Fatalf("GetBackendSelectionState(degraded) error = %v", err)
	}
	if item.State != types.BackendSelectionStateDegraded {
		t.Fatalf("state in degraded band = %q, want degraded", item.State)
	}

	item.SyntheticConfidence = 0.6
	item.RealSuccessScore = 0.6
	item.RealLatencyScore = 0.6
	if err := repo.SaveBackendSelectionState(item); err != nil {
		t.Fatalf("SaveBackendSelectionState(eligible) error = %v", err)
	}
	item, err = repo.GetBackendSelectionState("0xabc", "backend-a", "openai:chat-completions", "default")
	if err != nil {
		t.Fatalf("GetBackendSelectionState(eligible) error = %v", err)
	}
	if item.State != types.BackendSelectionStateEligible {
		t.Fatalf("state in eligible band = %q, want eligible", item.State)
	}
	if item.ExclusionReason != "" {
		t.Fatalf("eligible item exclusion reason = %q, want empty", item.ExclusionReason)
	}
}

func TestStateRepoApplyBackendOutcomeOpensCooldownAfterRepeatedBackendFailures(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	cfg := &config.Config{
		Members: []config.Member{
			{
				EthAddress: "0xabc",
				Backends: []config.Backend{
					{
						ID:        "backend-a",
						Transport: "http",
						Offerings: []config.Offering{
							{CapabilityID: "openai:chat-completions", OfferingID: "default"},
						},
					},
				},
			},
		},
	}
	if err := repo.SyncBackendSelectionStates(cfg); err != nil {
		t.Fatalf("SyncBackendSelectionStates() error = %v", err)
	}

	base := time.Date(2026, 5, 17, 16, 30, 0, 0, time.UTC)
	var updated types.BackendSelectionState
	for i := 0; i < 5; i++ {
		updated, err = repo.ApplyBackendOutcome(types.BackendOutcome{
			MemberEthAddress: "0xabc",
			BackendID:        "backend-a",
			CapabilityID:     "openai:chat-completions",
			OfferingID:       "default",
			Outcome:          types.BackendOutcomeBackendFailure,
			OccurredAt:       ptrTime(base.Add(time.Duration(i) * time.Minute)),
		})
		if err != nil {
			t.Fatalf("ApplyBackendOutcome(failure %d) error = %v", i, err)
		}
	}

	if updated.State != types.BackendSelectionStateExcluded {
		t.Fatalf("state after cooldown trigger = %q, want excluded", updated.State)
	}
	if updated.ExclusionReason != "pool_cooldown" {
		t.Fatalf("exclusion reason after cooldown trigger = %q, want pool_cooldown", updated.ExclusionReason)
	}
	if updated.RoutingReason != "pool_cooldown" {
		t.Fatalf("routing reason after cooldown trigger = %q, want pool_cooldown", updated.RoutingReason)
	}
	if updated.CooldownUntil == nil {
		t.Fatal("CooldownUntil = nil, want set")
	}
	wantCooldown := base.Add(9 * time.Minute)
	if !updated.CooldownUntil.Equal(wantCooldown) {
		t.Fatalf("CooldownUntil = %v, want %v", updated.CooldownUntil, wantCooldown)
	}
}

func TestStateRepoApplyBackendOutcomePrunesFailuresOutsideCooldownWindow(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	cfg := &config.Config{
		Members: []config.Member{
			{
				EthAddress: "0xabc",
				Backends: []config.Backend{
					{
						ID:        "backend-a",
						Transport: "http",
						Offerings: []config.Offering{
							{CapabilityID: "openai:chat-completions", OfferingID: "default"},
						},
					},
				},
			},
		},
	}
	if err := repo.SyncBackendSelectionStates(cfg); err != nil {
		t.Fatalf("SyncBackendSelectionStates() error = %v", err)
	}

	base := time.Date(2026, 5, 17, 16, 30, 0, 0, time.UTC)
	oldFailures := []time.Time{
		base.Add(-10 * time.Minute),
		base.Add(-9 * time.Minute),
		base.Add(-8 * time.Minute),
		base.Add(-7 * time.Minute),
	}
	for i, ts := range oldFailures {
		if _, err := repo.ApplyBackendOutcome(types.BackendOutcome{
			MemberEthAddress: "0xabc",
			BackendID:        "backend-a",
			CapabilityID:     "openai:chat-completions",
			OfferingID:       "default",
			Outcome:          types.BackendOutcomeBackendFailure,
			OccurredAt:       ptrTime(ts),
		}); err != nil {
			t.Fatalf("ApplyBackendOutcome(old failure %d) error = %v", i, err)
		}
	}

	updated, err := repo.ApplyBackendOutcome(types.BackendOutcome{
		MemberEthAddress: "0xabc",
		BackendID:        "backend-a",
		CapabilityID:     "openai:chat-completions",
		OfferingID:       "default",
		Outcome:          types.BackendOutcomeBackendFailure,
		OccurredAt:       ptrTime(base),
	})
	if err != nil {
		t.Fatalf("ApplyBackendOutcome(current failure) error = %v", err)
	}

	if updated.CooldownUntil != nil {
		t.Fatalf("CooldownUntil = %v, want nil when prior failures are outside 5m window", updated.CooldownUntil)
	}
	if updated.ExclusionReason == "pool_cooldown" {
		t.Fatalf("ExclusionReason = %q, want cooldown not to trigger from stale failures", updated.ExclusionReason)
	}
}

func TestStateRepoApplyBackendOutcomeDoesNotApplyImplicitTimeDecay(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	cfg := &config.Config{
		Members: []config.Member{
			{
				EthAddress: "0xabc",
				Backends: []config.Backend{
					{
						ID:        "backend-a",
						Transport: "http",
						Offerings: []config.Offering{
							{CapabilityID: "openai:chat-completions", OfferingID: "default"},
						},
					},
				},
			},
		},
	}
	if err := repo.SyncBackendSelectionStates(cfg); err != nil {
		t.Fatalf("SyncBackendSelectionStates() error = %v", err)
	}

	at := time.Date(2026, 5, 17, 16, 30, 0, 0, time.UTC)
	updated, err := repo.ApplyBackendOutcome(types.BackendOutcome{
		MemberEthAddress: "0xabc",
		BackendID:        "backend-a",
		CapabilityID:     "openai:chat-completions",
		OfferingID:       "default",
		Outcome:          types.BackendOutcomeSuccess,
		LatencyMetricMS:  700,
		OccurredAt:       ptrTime(at),
	})
	if err != nil {
		t.Fatalf("ApplyBackendOutcome(success) error = %v", err)
	}

	wantSuccess := updated.RealSuccessScore
	wantLatency := updated.RealLatencyScore
	wantEffective := updated.EffectiveSelectionScore

	later, err := repo.GetBackendSelectionState("0xabc", "backend-a", "openai:chat-completions", "default")
	if err != nil {
		t.Fatalf("GetBackendSelectionState() error = %v", err)
	}
	if math.Abs(later.RealSuccessScore-wantSuccess) > 1e-9 {
		t.Fatalf("RealSuccessScore on later read = %v, want unchanged %v without new observations", later.RealSuccessScore, wantSuccess)
	}
	if math.Abs(later.RealLatencyScore-wantLatency) > 1e-9 {
		t.Fatalf("RealLatencyScore on later read = %v, want unchanged %v without new observations", later.RealLatencyScore, wantLatency)
	}
	if math.Abs(later.EffectiveSelectionScore-wantEffective) > 1e-9 {
		t.Fatalf("EffectiveSelectionScore on later read = %v, want unchanged %v without new observations", later.EffectiveSelectionScore, wantEffective)
	}
}

func TestStateRepoSaveBackendSelectionStatePreservesOperatorOverrides(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	cfg := &config.Config{
		Members: []config.Member{
			{
				EthAddress: "0xabc",
				Backends: []config.Backend{
					{
						ID:        "backend-a",
						Transport: "http",
						Offerings: []config.Offering{
							{CapabilityID: "openai:chat-completions", OfferingID: "default"},
						},
					},
				},
			},
		},
	}
	if err := repo.SyncBackendSelectionStates(cfg); err != nil {
		t.Fatalf("SyncBackendSelectionStates() error = %v", err)
	}

	item, err := repo.GetBackendSelectionState("0xabc", "backend-a", "openai:chat-completions", "default")
	if err != nil {
		t.Fatalf("GetBackendSelectionState() error = %v", err)
	}
	item.State = types.BackendSelectionStateQuarantined
	item.ExclusionReason = "operator_quarantine"
	item.SyntheticConfidence = 0.9
	item.RealSuccessScore = 0.9
	item.RealLatencyScore = 0.9
	if err := repo.SaveBackendSelectionState(item); err != nil {
		t.Fatalf("SaveBackendSelectionState(operator quarantine) error = %v", err)
	}

	item, err = repo.GetBackendSelectionState("0xabc", "backend-a", "openai:chat-completions", "default")
	if err != nil {
		t.Fatalf("GetBackendSelectionState(after quarantine) error = %v", err)
	}
	if item.State != types.BackendSelectionStateQuarantined {
		t.Fatalf("quarantined state = %q, want quarantined", item.State)
	}
	if item.ExclusionReason != "operator_quarantine" {
		t.Fatalf("quarantine reason = %q, want operator_quarantine", item.ExclusionReason)
	}

	item.State = types.BackendSelectionStateExcluded
	item.ExclusionReason = "maintenance_window"
	item.SyntheticConfidence = 0.9
	item.RealSuccessScore = 0.9
	item.RealLatencyScore = 0.9
	override := 0.4
	item.WarmupOverride = &override
	if err := repo.SaveBackendSelectionState(item); err != nil {
		t.Fatalf("SaveBackendSelectionState(custom excluded) error = %v", err)
	}
	item, err = repo.GetBackendSelectionState("0xabc", "backend-a", "openai:chat-completions", "default")
	if err != nil {
		t.Fatalf("GetBackendSelectionState(after custom exclude) error = %v", err)
	}
	if item.State != types.BackendSelectionStateExcluded {
		t.Fatalf("custom excluded state = %q, want excluded", item.State)
	}
	if item.ExclusionReason != "maintenance_window" {
		t.Fatalf("custom exclusion reason = %q, want maintenance_window", item.ExclusionReason)
	}
	if item.WarmupOverride == nil || *item.WarmupOverride != 0.4 || item.WarmupSource != "manual_override" {
		t.Fatalf("manual warmup override not preserved: %#v", item)
	}
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
