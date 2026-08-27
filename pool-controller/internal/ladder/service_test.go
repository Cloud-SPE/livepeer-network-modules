package ladder

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/desiredstate"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

const (
	testMember     = "0x1111111111111111111111111111111111111111"
	testCapability = "openai:chat-completions"
	testOffering   = "default"
)

const testTemplateYAML = `id: chat-4090
capability: openai:chat-completions
offering_id: default
protocol: paid-job/v1
price_default:
  amount_wei: "5"
  per_units: 1
stacking:
  primary: true
`

// ladderFixture is one open repo plus the catalog the ladder reads
// templates from. The catalog is loaded from files, the way the
// controller loads it at boot, so a test cannot exercise a template the
// loader would have rejected.
func ladderFixture(t *testing.T, extraTemplates ...string) (*repo.StateRepo, *templates.Catalog) {
	t.Helper()
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })

	dir := t.TempDir()
	bodies := append([]string{testTemplateYAML}, extraTemplates...)
	for i, body := range bodies {
		name := filepath.Join(dir, fmt.Sprintf("tmpl-%02d.yaml", i))
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	catalog, err := templates.Load(dir)
	if err != nil {
		t.Fatalf("templates.Load() error = %v", err)
	}
	return stateRepo, catalog
}

// putPassedCertification records the run whose success is what lets a
// placement leave certification_testing. Without it the ladder is right
// to leave the placement where it is.
func putPassedCertification(t *testing.T, stateRepo *repo.StateRepo, assignmentID string, at time.Time) {
	t.Helper()
	if err := stateRepo.PutCertificationRun(types.CertificationRun{
		ID:           "cert-" + assignmentID,
		AssignmentID: assignmentID,
		Status:       types.CertificationPassed,
		StartedAt:    at.Add(-time.Minute),
		CompletedAt:  at,
	}); err != nil {
		t.Fatalf("PutCertificationRun() error = %v", err)
	}
}

func putAssignment(t *testing.T, stateRepo *repo.StateRepo, id string,
	state types.TemplateAssignmentState, templateID string) types.TemplateAssignment {
	t.Helper()
	assignment := types.TemplateAssignment{
		ID:               id,
		HardwareUnitID:   "gpu-" + id,
		HostEnrollmentID: "host-1",
		MemberEthAddress: testMember,
		TemplateID:       templateID,
		State:            state,
	}
	if err := stateRepo.PutTemplateAssignment(assignment); err != nil {
		t.Fatalf("PutTemplateAssignment(%s) error = %v", id, err)
	}
	return assignment
}

func serviceAt(stateRepo *repo.StateRepo, catalog *templates.Catalog,
	policy Policy, now time.Time) *Service {
	svc := New(stateRepo, catalog, policy)
	svc.now = func() time.Time { return now }
	return svc
}

func closeARound(t *testing.T, stateRepo *repo.StateRepo) {
	t.Helper()
	if err := stateRepo.PutSettlementWindow(types.SettlementWindow{
		ID: "window-1", Status: types.SettlementWindowApproved,
	}); err != nil {
		t.Fatalf("PutSettlementWindow() error = %v", err)
	}
}

func selectionStateFor(t *testing.T, stateRepo *repo.StateRepo,
	assignment types.TemplateAssignment) types.BackendSelectionState {
	t.Helper()
	state, err := stateRepo.GetBackendSelectionState(
		assignment.MemberEthAddress, BackendID(assignment), testCapability, testOffering)
	if err != nil {
		t.Fatalf("GetBackendSelectionState() error = %v", err)
	}
	return state
}

func ladderAuditEvents(t *testing.T, stateRepo *repo.StateRepo, assignmentID string) []types.AuditEvent {
	t.Helper()
	events, err := stateRepo.ListAuditEventsFiltered("ladder_transition", "template_assignment", assignmentID, 0)
	if err != nil {
		t.Fatalf("ListAuditEventsFiltered() error = %v", err)
	}
	return events
}

// TestBackendIDIsTheStringTheBrokerComposes pins the wire contract.
//
// The broker builds this id from the attach host id and the runner's
// local service name and looks the controller's row up by it; the
// controller builds it here. Neither side can tell the other it guessed
// wrong — a mismatch is silent, and shows up only as outcome reports
// that 404 and a ladder with no evidence to judge on.
func TestBackendIDIsTheStringTheBrokerComposes(t *testing.T) {
	assignment := types.TemplateAssignment{ID: "assign-1", HostEnrollmentID: "host-7"}
	want := "host-7" + "|" + desiredstate.ServiceName("assign-1")
	if got := BackendID(assignment); got != want {
		t.Fatalf("BackendID() = %q, want %q", got, want)
	}
	// Spelled out, so a change to either half is visible in the diff.
	if got := BackendID(assignment); got != "host-7|runner-assign-1" {
		t.Fatalf("BackendID() = %q, want %q", got, "host-7|runner-assign-1")
	}
}

// TestRunOnceSeedsSelectionStatePerPlacement covers the half of RunOnce
// that is not a decision. Nothing else creates these rows, and
// ApplyBackendOutcome refuses an unknown one, so a placement without a
// seeded row means the broker's outcome reports 404 forever.
func TestRunOnceSeedsSelectionStatePerPlacement(t *testing.T) {
	stateRepo, catalog := ladderFixture(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

	live := putAssignment(t, stateRepo, "assign-live", types.TemplateAssignmentActive, "chat-4090")
	retired := putAssignment(t, stateRepo, "assign-retired", types.TemplateAssignmentRetired, "chat-4090")
	// A placement naming a template this build does not ship has no
	// capability or offering to key a selection state on.
	unknown := putAssignment(t, stateRepo, "assign-unknown", types.TemplateAssignmentActive, "not-in-catalog")

	summary, err := serviceAt(stateRepo, catalog, testPolicy, now).RunOnce()
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if summary.Seeded != 1 {
		t.Fatalf("Seeded = %d, want 1", summary.Seeded)
	}
	if summary.Evaluated != 1 {
		t.Fatalf("Evaluated = %d, want 1 (retired skipped, uncatalogued skipped)", summary.Evaluated)
	}

	state := selectionStateFor(t, stateRepo, live)
	if state.BackendID != BackendID(live) {
		t.Fatalf("seeded backend_id = %q, want %q", state.BackendID, BackendID(live))
	}
	if state.CapabilityID != testCapability || state.OfferingID != testOffering {
		t.Fatalf("seeded state = %+v, want the template's capability/offering", state)
	}

	// A retired placement is finished: seeding it would advertise a
	// backend the pool will never route to again.
	if _, err := stateRepo.GetBackendSelectionState(
		retired.MemberEthAddress, BackendID(retired), testCapability, testOffering); err == nil {
		t.Fatal("a retired placement was seeded a selection state")
	}
	if _, err := stateRepo.GetBackendSelectionState(
		unknown.MemberEthAddress, BackendID(unknown), testCapability, testOffering); err == nil {
		t.Fatal("a placement with no catalogued template was seeded a selection state")
	}

	// Seeding is idempotent: a second pass must not re-seed, or the
	// summary would report churn that did not happen.
	second, err := serviceAt(stateRepo, catalog, testPolicy, now).RunOnce()
	if err != nil {
		t.Fatalf("second RunOnce() error = %v", err)
	}
	if second.Seeded != 0 {
		t.Fatalf("second pass Seeded = %d, want 0", second.Seeded)
	}
}

// TestRunOnceAppliesAPromotionEverywhereItHasToLand is the assertion
// that a promotion is real. A trust decision recorded on the assignment
// but not pushed into the weight the broker polls would be a promotion
// in name only.
func TestRunOnceAppliesAPromotionEverywhereItHasToLand(t *testing.T) {
	stateRepo, catalog := ladderFixture(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	assignment := putAssignment(t, stateRepo, "assign-1", types.TemplateAssignmentProbationary, "chat-4090")
	closeARound(t, stateRepo)

	// Seed the row first so the evidence (jobs served) is on it before
	// the ladder reads it.
	seeded, err := stateRepo.SeedBackendSelectionState(
		assignment.MemberEthAddress, BackendID(assignment), testCapability, testOffering)
	if err != nil {
		t.Fatalf("SeedBackendSelectionState() error = %v", err)
	}
	seeded.RecentRoutableOutcomeCount = testPolicy.ProbationMinJobs
	if err := stateRepo.SaveBackendSelectionState(seeded); err != nil {
		t.Fatalf("SaveBackendSelectionState() error = %v", err)
	}

	summary, err := serviceAt(stateRepo, catalog, testPolicy, now).RunOnce()
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(summary.Transitions) != 1 {
		t.Fatalf("Transitions = %+v, want exactly one", summary.Transitions)
	}
	transition := summary.Transitions[0]
	if transition.To != types.TemplateAssignmentActive || transition.ReasonCode != ReasonPromoted {
		t.Fatalf("transition = %+v, want a promotion to active", transition)
	}

	stored, err := stateRepo.GetTemplateAssignment("assign-1")
	if err != nil {
		t.Fatalf("GetTemplateAssignment() error = %v", err)
	}
	if stored.State != types.TemplateAssignmentActive {
		t.Fatalf("assignment state = %s, want active", stored.State)
	}
	if stored.ShareCapPPM != testPolicy.ActiveShareCapPPM {
		t.Fatalf("assignment share_cap_ppm = %d, want %d", stored.ShareCapPPM, testPolicy.ActiveShareCapPPM)
	}
	if !stored.ActivatedAt.Equal(now) {
		t.Fatalf("ActivatedAt = %s, want %s", stored.ActivatedAt, now)
	}

	// The weight the broker actually polls. Without this the runner
	// would be "active" and still capped at its probation slice.
	state := selectionStateFor(t, stateRepo, assignment)
	wantCap := float64(testPolicy.ActiveShareCapPPM) / 1_000_000
	if state.MaxShareCap != wantCap {
		t.Fatalf("selection state MaxShareCap = %v, want %v", state.MaxShareCap, wantCap)
	}
	if state.State != types.BackendSelectionStateEligible {
		t.Fatalf("selection state = %q, want eligible", state.State)
	}

	events := ladderAuditEvents(t, stateRepo, "assign-1")
	if len(events) != 1 {
		t.Fatalf("ladder_transition events = %d, want 1", len(events))
	}
	details := events[0].Details
	if details["from"] != string(types.TemplateAssignmentProbationary) ||
		details["to"] != string(types.TemplateAssignmentActive) {
		t.Fatalf("audit details = %+v, want probationary->active", details)
	}
	if details["reason_code"] != ReasonPromoted {
		t.Fatalf("audit reason_code = %v, want %q", details["reason_code"], ReasonPromoted)
	}
	// Evidence is what makes the record reviewable rather than just a
	// state change nobody can argue with.
	if evidence, _ := details["evidence"].(string); evidence == "" {
		t.Fatalf("audit event carries no evidence: %+v", details)
	}
	// The audit trail round-trips through JSON, so the number comes back
	// as a float64. Comparing it as one is the only way to assert the
	// share that was actually recorded rather than the Go type it had
	// before it was written.
	if share, ok := details["share_ppm"].(float64); !ok || uint64(share) != testPolicy.ActiveShareCapPPM {
		t.Fatalf("audit share_ppm = %#v, want %d", details["share_ppm"], testPolicy.ActiveShareCapPPM)
	}
}

// TestRunOnceHoldsPromotionUntilARoundHasClosed is the same placement
// with the same job count and no settled round. Jobs prove the runner
// answers; only a closed round proves the pool was paid.
func TestRunOnceHoldsPromotionUntilARoundHasClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  types.SettlementWindowStatus
		promote bool
	}{
		{name: "no window at all", status: "", promote: false},
		{name: "window still open", status: types.SettlementWindowOpen, promote: false},
		{name: "window awaiting a human", status: types.SettlementWindowPendingApproval, promote: false},
		{name: "window approved", status: types.SettlementWindowApproved, promote: true},
		{name: "window paid", status: types.SettlementWindowPaid, promote: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stateRepo, catalog := ladderFixture(t)
			now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
			assignment := putAssignment(t, stateRepo, "assign-1", types.TemplateAssignmentProbationary, "chat-4090")
			if tc.status != "" {
				if err := stateRepo.PutSettlementWindow(types.SettlementWindow{
					ID: "window-1", Status: tc.status,
				}); err != nil {
					t.Fatalf("PutSettlementWindow() error = %v", err)
				}
			}
			seeded, err := stateRepo.SeedBackendSelectionState(
				assignment.MemberEthAddress, BackendID(assignment), testCapability, testOffering)
			if err != nil {
				t.Fatalf("SeedBackendSelectionState() error = %v", err)
			}
			// Ten times the required jobs: the point is that no amount
			// of served work substitutes for having been paid.
			seeded.RecentRoutableOutcomeCount = testPolicy.ProbationMinJobs * 10
			if err := stateRepo.SaveBackendSelectionState(seeded); err != nil {
				t.Fatalf("SaveBackendSelectionState() error = %v", err)
			}

			summary, err := serviceAt(stateRepo, catalog, testPolicy, now).RunOnce()
			if err != nil {
				t.Fatalf("RunOnce() error = %v", err)
			}
			stored, _ := stateRepo.GetTemplateAssignment("assign-1")
			if tc.promote {
				if stored.State != types.TemplateAssignmentActive {
					t.Fatalf("state = %s, want active; transitions=%+v", stored.State, summary.Transitions)
				}
				return
			}
			if stored.State != types.TemplateAssignmentProbationary {
				t.Fatalf("state = %s, want it held at probationary; transitions=%+v",
					stored.State, summary.Transitions)
			}
			if len(summary.Transitions) != 0 {
				t.Fatalf("Transitions = %+v, want none", summary.Transitions)
			}
		})
	}
}

// TestRunOnceThrottlesOnAScoreBelowTheFloor exercises the score path
// end to end, including that the exploration share reaches the weight
// the broker polls — a throttled runner still has to see enough traffic
// to prove it recovered.
func TestRunOnceThrottlesOnAScoreBelowTheFloor(t *testing.T) {
	stateRepo, catalog := ladderFixture(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	assignment := putAssignment(t, stateRepo, "assign-1", types.TemplateAssignmentActive, "chat-4090")

	seeded, err := stateRepo.SeedBackendSelectionState(
		assignment.MemberEthAddress, BackendID(assignment), testCapability, testOffering)
	if err != nil {
		t.Fatalf("SeedBackendSelectionState() error = %v", err)
	}
	// The store recomputes the effective score from its parts, so set
	// the parts rather than the composite.
	seeded.SyntheticConfidence = 0.2
	seeded.RealSuccessScore = 0.2
	seeded.RealLatencyScore = 0.2
	if err := stateRepo.SaveBackendSelectionState(seeded); err != nil {
		t.Fatalf("SaveBackendSelectionState() error = %v", err)
	}

	summary, err := serviceAt(stateRepo, catalog, testPolicy, now).RunOnce()
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if len(summary.Transitions) != 1 || summary.Transitions[0].ReasonCode != ReasonThrottledLowScore {
		t.Fatalf("Transitions = %+v, want one throttle", summary.Transitions)
	}
	stored, _ := stateRepo.GetTemplateAssignment("assign-1")
	if stored.State != types.TemplateAssignmentThrottled {
		t.Fatalf("state = %s, want throttled", stored.State)
	}
	if stored.ShareCapPPM != testPolicy.ExplorationPPM {
		t.Fatalf("share_cap_ppm = %d, want the exploration share %d",
			stored.ShareCapPPM, testPolicy.ExplorationPPM)
	}
	state := selectionStateFor(t, stateRepo, assignment)
	wantCap := float64(testPolicy.ExplorationPPM) / 1_000_000
	if state.MaxShareCap != wantCap {
		t.Fatalf("selection state MaxShareCap = %v, want %v", state.MaxShareCap, wantCap)
	}
}

// TestRunOnceStartsProbationWithItsShareAndInFlightCap covers the entry
// rung: the small slice and the concurrency cap have to travel together
// so a runner that falls over under load does so cheaply.
func TestRunOnceStartsProbationWithItsShareAndInFlightCap(t *testing.T) {
	stateRepo, catalog := ladderFixture(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	assignment := putAssignment(t, stateRepo, "assign-1", types.TemplateAssignmentTesting, "chat-4090")
	putPassedCertification(t, stateRepo, "assign-1", now)

	if _, err := serviceAt(stateRepo, catalog, testPolicy, now).RunOnce(); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	stored, _ := stateRepo.GetTemplateAssignment("assign-1")
	if stored.State != types.TemplateAssignmentProbationary {
		t.Fatalf("state = %s, want probationary", stored.State)
	}
	if stored.ShareCapPPM != testPolicy.ProbationSharePPM {
		t.Fatalf("share_cap_ppm = %d, want %d", stored.ShareCapPPM, testPolicy.ProbationSharePPM)
	}
	if stored.MaxInFlight != testPolicy.ProbationMaxInFlight {
		t.Fatalf("max_in_flight = %d, want %d", stored.MaxInFlight, testPolicy.ProbationMaxInFlight)
	}
	if !stored.ProbationStartedAt.Equal(now) {
		t.Fatalf("ProbationStartedAt = %s, want %s", stored.ProbationStartedAt, now)
	}
	state := selectionStateFor(t, stateRepo, assignment)
	wantCap := float64(testPolicy.ProbationSharePPM) / 1_000_000
	if state.MaxShareCap != wantCap {
		t.Fatalf("selection state MaxShareCap = %v, want %v", state.MaxShareCap, wantCap)
	}
}

// TestApplySuspensionExcludesRatherThanZeroWeights is the distinction
// the apply path is built around. Zero is a weight, and a selector that
// refuses to starve anyone can round a weight back up; "excluded" is
// not a number and cannot be.
func TestApplySuspensionExcludesRatherThanZeroWeights(t *testing.T) {
	stateRepo, catalog := ladderFixture(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	assignment := putAssignment(t, stateRepo, "assign-1", types.TemplateAssignmentActive, "chat-4090")
	if _, err := stateRepo.SeedBackendSelectionState(
		assignment.MemberEthAddress, BackendID(assignment), testCapability, testOffering); err != nil {
		t.Fatalf("SeedBackendSelectionState() error = %v", err)
	}

	svc := serviceAt(stateRepo, catalog, testPolicy, now)
	transition := Transition{
		AssignmentID: "assign-1",
		From:         types.TemplateAssignmentActive,
		To:           types.TemplateAssignmentSuspended,
		ReasonCode:   ReasonSuspendedCertFails,
		Evidence:     "invalid output or fraud signal",
		At:           now,
	}
	if err := svc.apply(transition, now); err != nil {
		t.Fatalf("apply() error = %v", err)
	}

	stored, _ := stateRepo.GetTemplateAssignment("assign-1")
	if stored.State != types.TemplateAssignmentSuspended {
		t.Fatalf("assignment state = %s, want suspended", stored.State)
	}
	state := selectionStateFor(t, stateRepo, assignment)
	if state.State != types.BackendSelectionStateExcluded {
		t.Fatalf("selection state = %q, want %q — a suspended runner must be excluded, "+
			"not merely given a weight of zero", state.State, types.BackendSelectionStateExcluded)
	}
	if state.ExclusionReason != ReasonSuspendedCertFails {
		t.Fatalf("exclusion_reason = %q, want %q", state.ExclusionReason, ReasonSuspendedCertFails)
	}

	events := ladderAuditEvents(t, stateRepo, "assign-1")
	if len(events) != 1 || events[0].Details["to"] != string(types.TemplateAssignmentSuspended) {
		t.Fatalf("audit events = %+v, want one suspension record", events)
	}
}

// TestRunOnceDoesNotPromoteAPlacementWhoseCertificationIsStillRunning
// is a DELIBERATE FAILURE: it documents a real defect.
//
// certification_testing does not mean "certification passed" — the
// certification service sets it when a run STARTS, and sets
// probationary itself when the run passes. The ladder's testing arm
// nonetheless moves any placement in that state to probationary with
// the evidence string "certification passed", so:
//
//   - a run still in flight is promoted before it has proved anything,
//     with an audit record asserting a pass that never happened; and
//   - the ladder's own recertify decision (ReasonRecertifyFailures
//     parks a placement in certification_testing) is undone on the very
//     next tick, so K consecutive failures buys no re-certification at
//     all.
//
// The ladder needs to leave a placement in certification_testing alone
// and let the certification service move it, or gate the testing arm on
// a passed run.
func TestRunOnceDoesNotPromoteAPlacementWhoseCertificationIsStillRunning(t *testing.T) {
	stateRepo, catalog := ladderFixture(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	assignment := putAssignment(t, stateRepo, "assign-1", types.TemplateAssignmentTesting, "chat-4090")
	if err := stateRepo.PutCertificationRun(types.CertificationRun{
		ID:           "cert-assign-1",
		AssignmentID: assignment.ID,
		TemplateID:   "chat-4090",
		Status:       types.CertificationRunning,
		StartedAt:    now,
	}); err != nil {
		t.Fatalf("PutCertificationRun() error = %v", err)
	}

	if _, err := serviceAt(stateRepo, catalog, testPolicy, now).RunOnce(); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	stored, _ := stateRepo.GetTemplateAssignment("assign-1")
	if stored.State != types.TemplateAssignmentTesting {
		t.Fatalf("state = %s, want it left at certification_testing: the certification run "+
			"is still %s, so nothing has passed and the ladder must not claim it did",
			stored.State, types.CertificationRunning)
	}
}

// TestRecertifyDoesNotLeaveTheRunnerUncapped is a DELIBERATE FAILURE:
// it documents a second real defect on the same path.
//
// The apply path writes MaxShareCap = share_ppm / 1e6 and marks the
// selection state eligible for every destination except suspended. For
// a recertify transition the share is 0 — and the broker reads a
// max_share_cap of 0 as NO CAP, not as no traffic
// (capability-broker/internal/server/capability_group.go,
// applyMaxShareCaps: `if capLimit <= 0 ... continue`). So a placement
// sent back for re-certification after K consecutive failures is left
// eligible with its share bound removed, which is the opposite of what
// the transition means. The same reasoning the apply path already
// applies to suspension — "zero is a weight" — applies here.
func TestRecertifyDoesNotLeaveTheRunnerUncapped(t *testing.T) {
	stateRepo, catalog := ladderFixture(t)
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	assignment := putAssignment(t, stateRepo, "assign-1", types.TemplateAssignmentActive, "chat-4090")
	seeded, err := stateRepo.SeedBackendSelectionState(
		assignment.MemberEthAddress, BackendID(assignment), testCapability, testOffering)
	if err != nil {
		t.Fatalf("SeedBackendSelectionState() error = %v", err)
	}
	seeded.MaxShareCap = 0.15
	if err := stateRepo.SaveBackendSelectionState(seeded); err != nil {
		t.Fatalf("SaveBackendSelectionState() error = %v", err)
	}

	svc := serviceAt(stateRepo, catalog, testPolicy, now)
	if err := svc.apply(Transition{
		AssignmentID: "assign-1",
		From:         types.TemplateAssignmentActive,
		To:           types.TemplateAssignmentTesting,
		ReasonCode:   ReasonRecertifyFailures,
		Evidence:     "4 consecutive failures",
		At:           now,
	}, now); err != nil {
		t.Fatalf("apply() error = %v", err)
	}

	state := selectionStateFor(t, stateRepo, assignment)
	if state.State == types.BackendSelectionStateEligible && state.MaxShareCap == 0 {
		t.Fatalf("a placement sent back for re-certification is eligible with max_share_cap=0, "+
			"which the broker reads as UNCAPPED: state=%q max_share_cap=%v",
			state.State, state.MaxShareCap)
	}
}
