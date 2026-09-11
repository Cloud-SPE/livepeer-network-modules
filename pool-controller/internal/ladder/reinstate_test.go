package ladder

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// A reinstated placement must be judged on what it does NEXT.
//
// This is the trap that would otherwise make an operator's decision a
// visible no-op: a placement suspended after two failed certification
// runs, reinstated to certification_testing, would be re-suspended by
// the same historical count on the very next tick — once a minute,
// forever, with the operator watching it happen.
func TestCertificationEvidenceIgnoresRunsBeforeReinstate(t *testing.T) {
	base := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	reinstatedAt := base.Add(time.Hour)
	runs := []types.CertificationRun{
		{AssignmentID: "a", Status: types.CertificationFailed, CompletedAt: base},
		{AssignmentID: "a", Status: types.CertificationFailed, CompletedAt: base.Add(time.Minute)},
		// After the reinstate, and passing.
		{AssignmentID: "a", Status: types.CertificationPassed, CompletedAt: reinstatedAt.Add(time.Minute)},
	}

	before := certificationSince(runs, "a", time.Time{})
	if before.failures != 2 {
		t.Fatalf("without a boundary failures = %d, want the full history (2)", before.failures)
	}

	after := certificationSince(runs, "a", reinstatedAt)
	if after.failures != 0 {
		t.Fatalf("failures after reinstate = %d, want 0: the old failures are why it was suspended, "+
			"and counting them again re-suspends it immediately", after.failures)
	}
	if !after.passed {
		t.Fatal("the post-reinstate run passed and was not seen")
	}

	// And the ladder acts on it: a reinstated placement promotes rather
	// than bouncing straight back to suspended.
	assignment := types.TemplateAssignment{
		ID: "a", State: types.TemplateAssignmentTesting, ReinstatedAt: reinstatedAt,
	}
	got := Evaluate(assignment, Evidence{
		CertificationPassed:   after.passed,
		CertificationFailures: after.failures,
	}, DefaultPolicy, reinstatedAt.Add(2*time.Minute))
	if got == nil || got.To != types.TemplateAssignmentProbationary {
		t.Fatalf("Evaluate() = %+v, want a promotion to probationary", got)
	}
}

// A run exactly on the boundary belongs to the old life: the reinstate
// is the moment the slate is clean, not one tick after it.
func TestCertificationEvidenceExcludesTheBoundaryRun(t *testing.T) {
	at := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)
	got := certificationSince([]types.CertificationRun{
		{AssignmentID: "a", Status: types.CertificationFailed, CompletedAt: at},
	}, "a", at)
	if got.failures != 0 {
		t.Fatalf("failures = %d, want 0 for a run at the reinstate instant", got.failures)
	}
}
