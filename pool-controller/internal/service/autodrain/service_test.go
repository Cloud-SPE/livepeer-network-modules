package autodrain

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func newRepo(t *testing.T) *repo.StateRepo {
	t.Helper()
	r, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func seedActiveBackend(t *testing.T, r *repo.StateRepo, id string) types.MemberBackend {
	t.Helper()
	b := types.MemberBackend{
		ID:                 id,
		MemberID:           "member-1",
		Transport:          "http",
		URL:                "http://" + id,
		VerificationStatus: types.VerificationPassing,
		Status:             types.BackendStatusActive,
	}
	if err := r.PutMemberBackend(b); err != nil {
		t.Fatalf("PutMemberBackend() error = %v", err)
	}
	return b
}

func seedSelectionState(t *testing.T, r *repo.StateRepo, backendID, capability, offering string, routable, failures int) {
	t.Helper()
	if err := r.SaveBackendSelectionState(types.BackendSelectionState{
		MemberEthAddress:           "0xabc",
		BackendID:                  backendID,
		CapabilityID:               capability,
		OfferingID:                 offering,
		RecentRoutableOutcomeCount: routable,
		RecentBackendFailureCount:  failures,
	}); err != nil {
		t.Fatalf("SaveBackendSelectionState() error = %v", err)
	}
}

func TestRunOnce_DrainsBackendOverThreshold(t *testing.T) {
	r := newRepo(t)
	seedActiveBackend(t, r, "backend-bad")
	seedActiveBackend(t, r, "backend-good")
	seedSelectionState(t, r, "backend-bad", "rerank", "zerank", 20, 15) // 0.75 fail rate
	seedSelectionState(t, r, "backend-good", "rerank", "zerank", 20, 2) // 0.10 fail rate

	summary, err := RunOnce(r, Settings{FailureRateThreshold: 0.5, MinSamples: 10}, time.Now().UTC())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if summary.Drained != 1 || len(summary.DrainedIDs) != 1 || summary.DrainedIDs[0] != "backend-bad" {
		t.Fatalf("drained = %d ids=%v, want [backend-bad]", summary.Drained, summary.DrainedIDs)
	}
	if rate := summary.DrainedReason["backend-bad"]; rate < 0.74 || rate > 0.76 {
		t.Fatalf("drained reason rate = %v, want ~0.75", rate)
	}

	got, err := r.GetMemberBackend("backend-bad")
	if err != nil {
		t.Fatalf("GetMemberBackend(backend-bad) error = %v", err)
	}
	if got.Status != types.BackendStatusDraining {
		t.Fatalf("status = %q, want draining", got.Status)
	}
	good, err := r.GetMemberBackend("backend-good")
	if err != nil {
		t.Fatalf("GetMemberBackend(backend-good) error = %v", err)
	}
	if good.Status != types.BackendStatusActive {
		t.Fatalf("good backend status = %q, want active", good.Status)
	}
}

func TestRunOnce_SkipsBackendBelowMinSamples(t *testing.T) {
	r := newRepo(t)
	seedActiveBackend(t, r, "backend-quiet")
	// 100% failure rate but only 3 samples
	seedSelectionState(t, r, "backend-quiet", "rerank", "zerank", 3, 3)

	summary, err := RunOnce(r, Settings{FailureRateThreshold: 0.5, MinSamples: 10}, time.Now().UTC())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if summary.Drained != 0 {
		t.Fatalf("drained = %d, want 0 (below min samples)", summary.Drained)
	}
	got, err := r.GetMemberBackend("backend-quiet")
	if err != nil {
		t.Fatalf("GetMemberBackend() error = %v", err)
	}
	if got.Status != types.BackendStatusActive {
		t.Fatalf("status = %q, want active", got.Status)
	}
}

func TestRunOnce_DoesNotDrainAlreadyDraining(t *testing.T) {
	r := newRepo(t)
	seedActiveBackend(t, r, "backend-already")
	if err := r.SetMemberBackendStatus("backend-already", types.BackendStatusDraining); err != nil {
		t.Fatalf("SetMemberBackendStatus() error = %v", err)
	}
	seedSelectionState(t, r, "backend-already", "rerank", "zerank", 20, 15)

	summary, err := RunOnce(r, Settings{FailureRateThreshold: 0.5, MinSamples: 10}, time.Now().UTC())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if summary.Drained != 0 {
		t.Fatalf("drained = %d, want 0 (already draining)", summary.Drained)
	}
}

func TestRunOnce_WorstOfferingDrains(t *testing.T) {
	r := newRepo(t)
	seedActiveBackend(t, r, "backend-mixed")
	// One offering healthy, one bad — worst-case should drain.
	seedSelectionState(t, r, "backend-mixed", "rerank", "zerank", 20, 1)  // 0.05
	seedSelectionState(t, r, "backend-mixed", "embed", "default", 20, 18) // 0.90

	summary, err := RunOnce(r, Settings{FailureRateThreshold: 0.5, MinSamples: 10}, time.Now().UTC())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if summary.Drained != 1 {
		t.Fatalf("drained = %d, want 1 (worst-offering drains)", summary.Drained)
	}
}

func TestRunOnce_AuditEventEmitted(t *testing.T) {
	r := newRepo(t)
	seedActiveBackend(t, r, "backend-bad")
	seedSelectionState(t, r, "backend-bad", "rerank", "zerank", 20, 15)

	if _, err := RunOnce(r, Settings{FailureRateThreshold: 0.5, MinSamples: 10}, time.Now().UTC()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	events, err := r.ListAuditEvents()
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.Kind == AuditEventKind && ev.ResourceID == "backend-bad" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("audit event %q for resource %q not found", AuditEventKind, "backend-bad")
	}
}

func TestRunOnce_InvalidThresholdRejected(t *testing.T) {
	r := newRepo(t)
	for _, thr := range []float64{0, -0.1, 1.5} {
		if _, err := RunOnce(r, Settings{FailureRateThreshold: thr, MinSamples: 10}, time.Now().UTC()); err == nil {
			t.Fatalf("RunOnce(threshold=%v) expected error, got nil", thr)
		}
	}
}
