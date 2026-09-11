package ladder

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// testPolicy is deliberately not DefaultPolicy: every number differs
// from the default, so a test that passes here cannot be passing
// because the code silently substituted a default the caller did not
// ask for.
var testPolicy = Policy{
	ProbationSharePPM:      30_000,
	ProbationMaxInFlight:   2,
	ProbationMinJobs:       10,
	ExplorationPPM:         60_000,
	ScoreFloor:             0.40,
	RecertifyAfterFailures: 4,
	ActiveShareCapPPM:      200_000,
}

func assignment(state types.TemplateAssignmentState) types.TemplateAssignment {
	return types.TemplateAssignment{ID: "assign-1", State: state}
}

func TestEvaluateStateMachine(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name        string
		state       types.TemplateAssignmentState
		evidence    Evidence
		wantNil     bool
		wantTo      types.TemplateAssignmentState
		wantReason  string
		wantShare   uint64
		wantInFlght int
	}{
		{
			// Certification passing is what puts a placement in testing;
			// leaving it there would mean the pool never buys the
			// evidence promotion needs.
			name:        "testing starts probation on the probation share and in-flight cap",
			state:       types.TemplateAssignmentTesting,
			evidence:    Evidence{CertificationPassed: true},
			wantTo:      types.TemplateAssignmentProbationary,
			wantReason:  ReasonProbationStarted,
			wantShare:   testPolicy.ProbationSharePPM,
			wantInFlght: testPolicy.ProbationMaxInFlight,
		},
		{
			// Both halves of the evidence are present.
			name:       "probationary promotes on jobs AND a closed round",
			state:      types.TemplateAssignmentProbationary,
			evidence:   Evidence{JobsServed: testPolicy.ProbationMinJobs, RoundClosed: true},
			wantTo:     types.TemplateAssignmentActive,
			wantReason: ReasonPromoted,
			wantShare:  testPolicy.ActiveShareCapPPM,
		},
		{
			// Jobs prove the runner answers. Only a closed round proves
			// the pool was actually paid for those answers, and being
			// paid is the thing promotion risks — so a pile of served
			// jobs on its own must not move anything.
			name:     "probationary holds on jobs alone with no closed round",
			state:    types.TemplateAssignmentProbationary,
			evidence: Evidence{JobsServed: testPolicy.ProbationMinJobs * 10, RoundClosed: false},
			wantNil:  true,
		},
		{
			// The mirror image: the pool was paid, but not for enough
			// work by this runner to say anything about it.
			name:     "probationary holds on a closed round with too few jobs",
			state:    types.TemplateAssignmentProbationary,
			evidence: Evidence{JobsServed: testPolicy.ProbationMinJobs - 1, RoundClosed: true},
			wantNil:  true,
		},
		{
			name:       "probationary suspends after repeated certification failure",
			state:      types.TemplateAssignmentProbationary,
			evidence:   Evidence{CertificationFailures: 2, JobsServed: 1000, RoundClosed: true},
			wantTo:     types.TemplateAssignmentSuspended,
			wantReason: ReasonSuspendedCertFails,
			wantShare:  0,
		},
		{
			name:       "active throttles below the score floor onto the exploration share",
			state:      types.TemplateAssignmentActive,
			evidence:   Evidence{CompositeScore: testPolicy.ScoreFloor - 0.01},
			wantTo:     types.TemplateAssignmentThrottled,
			wantReason: ReasonThrottledLowScore,
			wantShare:  testPolicy.ExplorationPPM,
		},
		{
			// The floor is a floor, not a ceiling: exactly at it is
			// still good enough to stay active.
			name:     "active holds exactly at the score floor",
			state:    types.TemplateAssignmentActive,
			evidence: Evidence{CompositeScore: testPolicy.ScoreFloor},
			wantNil:  true,
		},
		{
			name:       "throttled recovers to active once the score is back at the floor",
			state:      types.TemplateAssignmentThrottled,
			evidence:   Evidence{CompositeScore: testPolicy.ScoreFloor},
			wantTo:     types.TemplateAssignmentActive,
			wantReason: ReasonRecovered,
			wantShare:  testPolicy.ActiveShareCapPPM,
		},
		{
			name:     "throttled stays throttled while still under the floor",
			state:    types.TemplateAssignmentThrottled,
			evidence: Evidence{CompositeScore: testPolicy.ScoreFloor - 0.01},
			wantNil:  true,
		},
		{
			// Consecutive failures outrank a score that still looks
			// fine: a run of failures is a pattern, and the answer to a
			// pattern is to re-prove the runner, not to average it away.
			name:       "active recertifies at the consecutive-failure count",
			state:      types.TemplateAssignmentActive,
			evidence:   Evidence{ConsecutiveFailures: testPolicy.RecertifyAfterFailures, CompositeScore: 1.0},
			wantTo:     types.TemplateAssignmentTesting,
			wantReason: ReasonRecertifyFailures,
			wantShare:  0,
		},
		{
			name:       "throttled recertifies at the consecutive-failure count",
			state:      types.TemplateAssignmentThrottled,
			evidence:   Evidence{ConsecutiveFailures: testPolicy.RecertifyAfterFailures, CompositeScore: 1.0},
			wantTo:     types.TemplateAssignmentTesting,
			wantReason: ReasonRecertifyFailures,
			wantShare:  0,
		},
		{
			name:     "active one failure short of the threshold does not recertify",
			state:    types.TemplateAssignmentActive,
			evidence: Evidence{ConsecutiveFailures: testPolicy.RecertifyAfterFailures - 1, CompositeScore: 1.0},
			wantNil:  true,
		},
		{
			// States the ladder does not judge. A placement that is
			// pending, draining or being re-imaged is somebody else's
			// decision in flight; the ladder must not fight it.
			name:     "pending is not the ladder's business",
			state:    types.TemplateAssignmentPending,
			evidence: Evidence{JobsServed: 1000, RoundClosed: true},
			wantNil:  true,
		},
		{
			name:     "draining is not the ladder's business",
			state:    types.TemplateAssignmentDraining,
			evidence: Evidence{JobsServed: 1000, RoundClosed: true},
			wantNil:  true,
		},
		{
			name:     "retired is not the ladder's business",
			state:    types.TemplateAssignmentRetired,
			evidence: Evidence{JobsServed: 1000, RoundClosed: true},
			wantNil:  true,
		},
		{
			name:     "update_required is not the ladder's business",
			state:    types.TemplateAssignmentUpdateRequired,
			evidence: Evidence{JobsServed: 1000, RoundClosed: true},
			wantNil:  true,
		},
		{
			// A suspended placement is waiting on a person. The ladder
			// re-running must not quietly walk it back out.
			name:     "suspended stays suspended without an operator",
			state:    types.TemplateAssignmentSuspended,
			evidence: Evidence{JobsServed: 1000, RoundClosed: true, CompositeScore: 1.0},
			wantNil:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(assignment(tc.state), tc.evidence, testPolicy, now)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("Evaluate() = %+v, want nil", *got)
				}
				return
			}
			if got == nil {
				t.Fatalf("Evaluate() = nil, want a transition to %s", tc.wantTo)
			}
			if got.From != tc.state || got.To != tc.wantTo {
				t.Fatalf("Evaluate() moved %s->%s, want %s->%s", got.From, got.To, tc.state, tc.wantTo)
			}
			if got.ReasonCode != tc.wantReason {
				t.Fatalf("reason_code = %q, want %q", got.ReasonCode, tc.wantReason)
			}
			if got.SharePPM != tc.wantShare {
				t.Fatalf("share_ppm = %d, want %d", got.SharePPM, tc.wantShare)
			}
			if got.MaxInFlight != tc.wantInFlght {
				t.Fatalf("max_in_flight = %d, want %d", got.MaxInFlight, tc.wantInFlght)
			}
			// Evidence is not decoration: it is what an operator and the
			// member both read to understand why the pool moved them, so
			// a transition without it is not reviewable.
			if got.Evidence == "" {
				t.Fatalf("transition to %s carries no evidence string", got.To)
			}
			if got.AssignmentID != "assign-1" {
				t.Fatalf("assignment_id = %q", got.AssignmentID)
			}
			if !got.At.Equal(now) {
				t.Fatalf("At = %s, want %s", got.At, now)
			}
		})
	}
}

// TestSeriousFailureSuspendsFromAnyStateAndOutranksScore pins the one
// rule that is not a matter of degree. An invalid-output or fraud
// signal is not a number to be averaged against a good score — one is
// enough, from wherever the placement happens to be standing.
func TestSeriousFailureSuspendsFromAnyStateAndOutranksScore(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	states := []types.TemplateAssignmentState{
		types.TemplateAssignmentPending,
		types.TemplateAssignmentTesting,
		types.TemplateAssignmentProbationary,
		types.TemplateAssignmentActive,
		types.TemplateAssignmentThrottled,
		types.TemplateAssignmentDraining,
		types.TemplateAssignmentUpdateRequired,
		types.TemplateAssignmentRetired,
	}
	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			// A perfect score and no failure history at all: the only
			// thing wrong is the serious failure, and it must still win.
			ev := Evidence{
				SeriousFailure:      true,
				CompositeScore:      1.0,
				ConsecutiveFailures: 0,
				JobsServed:          10_000,
				RoundClosed:         true,
			}
			got := Evaluate(assignment(state), ev, testPolicy, now)
			if got == nil {
				t.Fatalf("Evaluate() = nil, want suspension from %s", state)
			}
			if got.To != types.TemplateAssignmentSuspended {
				t.Fatalf("Evaluate() moved to %s, want suspended", got.To)
			}
			// Suspension must strip the traffic with it. Recording the
			// state without dropping the share would suspend a runner
			// on paper while it kept serving.
			if got.SharePPM != 0 || got.MaxInFlight != 0 {
				t.Fatalf("suspension left share_ppm=%d max_in_flight=%d, want 0/0",
					got.SharePPM, got.MaxInFlight)
			}
		})
	}

	// Already suspended: nothing more to do, and re-emitting would put
	// a duplicate entry in the audit trail every tick.
	if got := Evaluate(assignment(types.TemplateAssignmentSuspended),
		Evidence{SeriousFailure: true}, testPolicy, now); got != nil {
		t.Fatalf("Evaluate() on an already-suspended placement = %+v, want nil", *got)
	}
}

// TestWithDefaultsTreatsZeroAsUnconfigured is the reason WithDefaults
// exists. A pool that writes down only the one number it cares about
// must not have every other number read as a deliberate zero — a
// probation share of zero makes promotion impossible by starving the
// very evidence promotion requires.
func TestWithDefaultsTreatsZeroAsUnconfigured(t *testing.T) {
	t.Run("empty policy is the default policy", func(t *testing.T) {
		if got := (Policy{}).WithDefaults(); got != DefaultPolicy {
			t.Fatalf("Policy{}.WithDefaults() = %+v, want %+v", got, DefaultPolicy)
		}
	})

	t.Run("a score floor alone still gets a usable probation share", func(t *testing.T) {
		got := Policy{ScoreFloor: 0.55}.WithDefaults()
		if got.ScoreFloor != 0.55 {
			t.Fatalf("ScoreFloor = %v, want the configured 0.55", got.ScoreFloor)
		}
		if got.ProbationSharePPM == 0 {
			t.Fatal("ProbationSharePPM = 0: a runner with no share can never serve the " +
				"jobs promotion requires, so it would sit in probation forever")
		}
		if got.ProbationSharePPM != DefaultPolicy.ProbationSharePPM {
			t.Fatalf("ProbationSharePPM = %d, want the default %d",
				got.ProbationSharePPM, DefaultPolicy.ProbationSharePPM)
		}
		if got.ProbationMinJobs != DefaultPolicy.ProbationMinJobs ||
			got.ProbationMaxInFlight != DefaultPolicy.ProbationMaxInFlight ||
			got.ExplorationPPM != DefaultPolicy.ExplorationPPM ||
			got.RecertifyAfterFailures != DefaultPolicy.RecertifyAfterFailures ||
			got.ActiveShareCapPPM != DefaultPolicy.ActiveShareCapPPM {
			t.Fatalf("unset fields did not default: %+v", got)
		}
	})

	t.Run("a fully configured policy is left alone", func(t *testing.T) {
		if got := testPolicy.WithDefaults(); got != testPolicy {
			t.Fatalf("WithDefaults() = %+v, want it unchanged %+v", got, testPolicy)
		}
	})

	t.Run("evaluate applies defaults to a partial policy", func(t *testing.T) {
		// Evaluate calls WithDefaults itself, so a caller that passes a
		// partial policy gets the same treatment as one that filled it
		// in first.
		now := time.Now().UTC()
		got := Evaluate(assignment(types.TemplateAssignmentTesting),
			Evidence{CertificationPassed: true}, Policy{ScoreFloor: 0.55}, now)
		if got == nil || got.SharePPM != DefaultPolicy.ProbationSharePPM {
			t.Fatalf("Evaluate() with a partial policy = %+v, want share %d",
				got, DefaultPolicy.ProbationSharePPM)
		}
	})
}

func TestEvaluateAllIsDeterministicAndSilentWhenNothingMoves(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

	t.Run("nothing to report when nothing moves", func(t *testing.T) {
		items := []types.TemplateAssignment{
			{ID: "a", State: types.TemplateAssignmentActive},
			{ID: "b", State: types.TemplateAssignmentProbationary},
			{ID: "c", State: types.TemplateAssignmentRetired},
		}
		evidence := map[string]Evidence{
			"a": {CompositeScore: 0.9},
			"b": {JobsServed: 1},
		}
		got := EvaluateAll(items, evidence, testPolicy, now)
		if len(got) != 0 {
			t.Fatalf("EvaluateAll() = %+v, want no transitions", got)
		}
	})

	t.Run("transitions come back sorted by assignment id whatever the input order", func(t *testing.T) {
		// The order matters because the transitions are written to the
		// audit trail in this order: a set that reshuffled between runs
		// would make two identical ladder passes look different.
		items := []types.TemplateAssignment{
			{ID: "zeta", State: types.TemplateAssignmentTesting},
			{ID: "alpha", State: types.TemplateAssignmentTesting},
			{ID: "mid", State: types.TemplateAssignmentTesting},
		}
		want := []string{"alpha", "mid", "zeta"}
		for i := 0; i < 5; i++ {
			// A passing run per placement, because promotion out of
			// testing now requires one — the state means "being
			// tested", not "passed".
			passed := map[string]Evidence{
				"zeta": {CertificationPassed: true}, "alpha": {CertificationPassed: true},
				"mid": {CertificationPassed: true},
			}
			got := EvaluateAll(items, passed, testPolicy, now)
			if len(got) != 3 {
				t.Fatalf("EvaluateAll() returned %d transitions, want 3", len(got))
			}
			for j, id := range want {
				if got[j].AssignmentID != id {
					t.Fatalf("transition[%d] = %q, want %q", j, got[j].AssignmentID, id)
				}
			}
		}
	})

	t.Run("the input slice is not reordered under the caller", func(t *testing.T) {
		// EvaluateAll sorts a copy. Sorting the caller's slice in place
		// would silently reorder whatever list the caller goes on to
		// use for something else.
		items := []types.TemplateAssignment{
			{ID: "zeta", State: types.TemplateAssignmentActive},
			{ID: "alpha", State: types.TemplateAssignmentActive},
		}
		_ = EvaluateAll(items, map[string]Evidence{}, testPolicy, now)
		if items[0].ID != "zeta" || items[1].ID != "alpha" {
			t.Fatalf("caller's slice was reordered: %q, %q", items[0].ID, items[1].ID)
		}
	})

	t.Run("a placement with no evidence entry is judged on the zero evidence", func(t *testing.T) {
		// map lookup of a missing key yields the zero Evidence, which
		// for a probationary placement means "no jobs, no round" and so
		// no promotion — the safe reading of missing evidence.
		items := []types.TemplateAssignment{{ID: "a", State: types.TemplateAssignmentProbationary}}
		if got := EvaluateAll(items, map[string]Evidence{}, testPolicy, now); len(got) != 0 {
			t.Fatalf("EvaluateAll() = %+v, want no transition without evidence", got)
		}
	})
}
