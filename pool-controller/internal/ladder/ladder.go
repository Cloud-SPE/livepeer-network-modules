// Package ladder moves a placement between trust states.
//
// A newly certified runner has proved it can serve the workload once.
// That is not the same as having proved it will keep doing so, so it
// starts on a small share of real traffic and earns its way up. The
// ladder is what turns "it passed a smoke test" into "the pool routes
// meaningful money through it", and every step it takes is recorded
// with the evidence that justified it (plan 0044 §3.5).
package ladder

import (
	"fmt"
	"sort"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// Reason codes. Each names the evidence that moved a placement, so an
// operator reading an audit trail and a member reading their portal see
// the same sentence.
const (
	ReasonProbationStarted   = "probation_started"
	ReasonPromoted           = "promoted_to_active"
	ReasonThrottledLowScore  = "throttled_score_below_floor"
	ReasonRecovered          = "recovered_to_active"
	ReasonRecertifyFailures  = "recertify_consecutive_failures"
	ReasonSuspendedCertFails = "suspended_repeated_certification_failure"
)

// Policy is the pool's ladder configuration (plan 0044 §3.5).
type Policy struct {
	// ProbationSharePPM is the slice of an offering's traffic a
	// probationary runner may take. Small on purpose: the pool is
	// buying evidence, and it should not pay much for it.
	ProbationSharePPM uint64
	// ProbationMaxInFlight caps concurrency during probation, so a
	// runner that falls over under load does so cheaply.
	ProbationMaxInFlight int
	// ProbationMinJobs is how much evidence promotion requires.
	ProbationMinJobs int
	// ExplorationPPM is traffic deliberately spent on runners the pool
	// is less sure of, so a scoring system cannot starve a recovering
	// runner of the very traffic it needs to prove itself.
	ExplorationPPM uint64
	// ScoreFloor is the composite below which a runner is throttled.
	ScoreFloor float64
	// RecertifyAfterFailures is the consecutive-failure count that
	// sends a runner back for re-certification.
	RecertifyAfterFailures int
	// ActiveShareCapPPM bounds any one runner's share once active.
	ActiveShareCapPPM uint64
}

// DefaultPolicy is what a pool gets when it configures nothing. The
// numbers come from plan 0040 §8.3: a probationary runner takes about
// two percent of an offering at concurrency one, and needs twenty jobs
// and a completed settlement round before it is trusted with more.
var DefaultPolicy = Policy{
	ProbationSharePPM:      20_000,
	ProbationMaxInFlight:   1,
	ProbationMinJobs:       20,
	ExplorationPPM:         50_000,
	ScoreFloor:             0.30,
	RecertifyAfterFailures: 5,
	ActiveShareCapPPM:      150_000,
}

// WithDefaults fills unset fields. A zero is "not configured" rather
// than "zero": a pool that sets only a score floor should not
// accidentally get a probation share of nothing, which would make
// promotion impossible by starving the evidence.
func (p Policy) WithDefaults() Policy {
	out := p
	if out.ProbationSharePPM == 0 {
		out.ProbationSharePPM = DefaultPolicy.ProbationSharePPM
	}
	if out.ProbationMaxInFlight == 0 {
		out.ProbationMaxInFlight = DefaultPolicy.ProbationMaxInFlight
	}
	if out.ProbationMinJobs == 0 {
		out.ProbationMinJobs = DefaultPolicy.ProbationMinJobs
	}
	if out.ExplorationPPM == 0 {
		out.ExplorationPPM = DefaultPolicy.ExplorationPPM
	}
	if out.ScoreFloor == 0 {
		out.ScoreFloor = DefaultPolicy.ScoreFloor
	}
	if out.RecertifyAfterFailures == 0 {
		out.RecertifyAfterFailures = DefaultPolicy.RecertifyAfterFailures
	}
	if out.ActiveShareCapPPM == 0 {
		out.ActiveShareCapPPM = DefaultPolicy.ActiveShareCapPPM
	}
	return out
}

// Evidence is what the ladder judges a placement on.
type Evidence struct {
	// JobsServed since probation began.
	JobsServed int
	// RoundClosed says a settlement window covering this placement has
	// closed. Promotion needs it as well as the job count: jobs prove
	// the runner works, a closed round proves the pool was actually
	// paid for them.
	RoundClosed bool
	// CompositeScore is the selection score, 0..1.
	CompositeScore float64
	// ConsecutiveFailures since the last success.
	ConsecutiveFailures int
	// SeriousFailure is an invalid-output or fraud signal. It is kept
	// separate from the failure count because it is not a matter of
	// degree — one is enough.
	SeriousFailure bool
	// CertificationFailures is how many times certification has failed
	// for this placement.
	CertificationFailures int
}

// Transition is one step, with the evidence that justified it.
type Transition struct {
	AssignmentID string                        `json:"assignment_id"`
	From         types.TemplateAssignmentState `json:"from"`
	To           types.TemplateAssignmentState `json:"to"`
	ReasonCode   string                        `json:"reason_code"`
	Evidence     string                        `json:"evidence"`
	At           time.Time                     `json:"at"`
	// SharePPM and MaxInFlight are what the broker should be told after
	// this step. They ride with the transition so the push and the
	// record cannot disagree.
	SharePPM    uint64 `json:"share_ppm"`
	MaxInFlight int    `json:"max_in_flight,omitempty"`
}

// Evaluate decides whether one placement should move.
//
// It returns nil when nothing should change, which is the common case —
// the ladder is evaluated far more often than it moves.
func Evaluate(assignment types.TemplateAssignment, ev Evidence, policy Policy, now time.Time) *Transition {
	policy = policy.WithDefaults()
	step := func(to types.TemplateAssignmentState, reason, evidence string, share uint64, maxInFlight int) *Transition {
		return &Transition{
			AssignmentID: assignment.ID, From: assignment.State, To: to,
			ReasonCode: reason, Evidence: evidence, At: now,
			SharePPM: share, MaxInFlight: maxInFlight,
		}
	}

	// A serious failure outranks everything. It is not a score to be
	// averaged away — it is a reason to stop sending this runner work
	// until a person has looked.
	if ev.SeriousFailure && assignment.State != types.TemplateAssignmentSuspended {
		return step(types.TemplateAssignmentSuspended, ReasonSuspendedCertFails,
			"invalid output or fraud signal", 0, 0)
	}

	switch assignment.State {
	case types.TemplateAssignmentTesting:
		// Certification passed; start earning on a small share.
		return step(types.TemplateAssignmentProbationary, ReasonProbationStarted,
			"certification passed", policy.ProbationSharePPM, policy.ProbationMaxInFlight)

	case types.TemplateAssignmentProbationary:
		if ev.CertificationFailures >= 2 {
			return step(types.TemplateAssignmentSuspended, ReasonSuspendedCertFails,
				fmt.Sprintf("%d certification failures", ev.CertificationFailures), 0, 0)
		}
		// Both halves are required. Jobs alone prove the runner
		// answers; a closed round proves the pool was paid for the
		// answers, which is the thing promotion actually risks.
		if ev.JobsServed >= policy.ProbationMinJobs && ev.RoundClosed {
			return step(types.TemplateAssignmentActive, ReasonPromoted,
				fmt.Sprintf("%d jobs served and a settlement round closed", ev.JobsServed),
				policy.ActiveShareCapPPM, 0)
		}
		return nil

	case types.TemplateAssignmentActive:
		if ev.ConsecutiveFailures >= policy.RecertifyAfterFailures {
			return step(types.TemplateAssignmentTesting, ReasonRecertifyFailures,
				fmt.Sprintf("%d consecutive failures", ev.ConsecutiveFailures), 0, 0)
		}
		if ev.CompositeScore < policy.ScoreFloor {
			return step(types.TemplateAssignmentThrottled, ReasonThrottledLowScore,
				fmt.Sprintf("composite score %.2f below floor %.2f", ev.CompositeScore, policy.ScoreFloor),
				policy.ExplorationPPM, 0)
		}
		return nil

	case types.TemplateAssignmentThrottled:
		if ev.ConsecutiveFailures >= policy.RecertifyAfterFailures {
			return step(types.TemplateAssignmentTesting, ReasonRecertifyFailures,
				fmt.Sprintf("%d consecutive failures", ev.ConsecutiveFailures), 0, 0)
		}
		// Recovery needs the score back above the floor. The
		// exploration share exists so a throttled runner still sees
		// enough traffic to demonstrate that.
		if ev.CompositeScore >= policy.ScoreFloor {
			return step(types.TemplateAssignmentActive, ReasonRecovered,
				fmt.Sprintf("composite score %.2f recovered above floor %.2f", ev.CompositeScore, policy.ScoreFloor),
				policy.ActiveShareCapPPM, 0)
		}
		return nil
	}
	return nil
}

// EvaluateAll runs the ladder over a set, in a deterministic order.
func EvaluateAll(assignments []types.TemplateAssignment, evidence map[string]Evidence,
	policy Policy, now time.Time) []Transition {

	items := append([]types.TemplateAssignment(nil), assignments...)
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	out := make([]Transition, 0)
	for _, assignment := range items {
		if t := Evaluate(assignment, evidence[assignment.ID], policy, now); t != nil {
			out = append(out, *t)
		}
	}
	return out
}
