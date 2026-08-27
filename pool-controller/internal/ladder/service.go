package ladder

import (
	"fmt"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/desiredstate"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// BackendID is the id the broker knows a placed runner by.
//
// It must match exactly what the broker uses when it reports an outcome
// or reads a pool snapshot, because both sides look each other up by
// this string and neither can tell the other it guessed wrong. The
// broker composes it from the attach host id and the runner's local id;
// the controller knows the first as the enrollment id, and the agent
// derives the second from the assignment.
//
// The parts are joined with "|" and never split apart again — the
// selection-state key joins on the same character, so treating this as
// parseable would be ambiguous.
func BackendID(assignment types.TemplateAssignment) string {
	return assignment.HostEnrollmentID + "|" + desiredstate.ServiceName(assignment.ID)
}

// Service runs the ladder against stored state.
type Service struct {
	repo    *repo.StateRepo
	catalog *templates.Catalog
	policy  Policy
	now     func() time.Time
}

func New(stateRepo *repo.StateRepo, catalog *templates.Catalog, policy Policy) *Service {
	return &Service{repo: stateRepo, catalog: catalog, policy: policy.WithDefaults(),
		now: func() time.Time { return time.Now().UTC() }}
}

// Summary is what one pass did.
type Summary struct {
	Seeded      int          `json:"seeded"`
	Evaluated   int          `json:"evaluated"`
	Transitions []Transition `json:"transitions"`
}

// RunOnce seeds selection state for every placement, judges each, and
// applies what moved.
func (s *Service) RunOnce() (Summary, error) {
	var summary Summary
	assignments, err := s.repo.ListTemplateAssignments()
	if err != nil {
		return summary, err
	}
	states, err := s.repo.ListBackendSelectionStates()
	if err != nil {
		return summary, err
	}
	byKey := make(map[string]types.BackendSelectionState, len(states))
	for _, state := range states {
		byKey[state.Key] = state
	}
	windows, err := s.repo.ListSettlementWindows()
	if err != nil {
		return summary, err
	}
	closedRound := hasClosedWindow(windows)

	now := s.now()
	evidence := make(map[string]Evidence, len(assignments))
	live := make([]types.TemplateAssignment, 0, len(assignments))
	for _, assignment := range assignments {
		if assignment.State == types.TemplateAssignmentRetired {
			continue
		}
		tmpl, known := s.catalog.Get(assignment.TemplateID)
		if !known {
			continue
		}
		key := selectionKey(assignment, tmpl)
		state, exists := byKey[key]
		if !exists {
			// Seed it. Nothing else creates these rows now that the
			// legacy member model is gone, and ApplyBackendOutcome
			// refuses an unknown one — so without this the broker's
			// outcome reports would 404 forever and the ladder would
			// have no evidence to judge on.
			seeded, err := s.repo.SeedBackendSelectionState(
				assignment.MemberEthAddress, BackendID(assignment), tmpl.Capability, tmpl.OfferingID)
			if err != nil {
				return summary, fmt.Errorf("seed selection state for %s: %w", assignment.ID, err)
			}
			state = seeded
			summary.Seeded++
		}
		evidence[assignment.ID] = Evidence{
			JobsServed:          state.RecentRoutableOutcomeCount,
			RoundClosed:         closedRound,
			CompositeScore:      state.EffectiveSelectionScore,
			ConsecutiveFailures: state.RecentBackendFailureCount,
		}
		live = append(live, assignment)
	}
	summary.Evaluated = len(live)

	transitions := EvaluateAll(live, evidence, s.policy, now)
	for _, transition := range transitions {
		if err := s.apply(transition, now); err != nil {
			return summary, err
		}
	}
	summary.Transitions = transitions
	return summary, nil
}

// apply records a transition and pushes its consequences.
//
// The share and cap are written into the selection state the broker
// polls, so the trust decision and the traffic it entitles a runner to
// travel together — a promotion that did not change the weight would be
// a promotion in name only.
func (s *Service) apply(transition Transition, now time.Time) error {
	assignment, err := s.repo.GetTemplateAssignment(transition.AssignmentID)
	if err != nil {
		return err
	}
	assignment.State = transition.To
	assignment.UpdatedAt = now
	switch transition.To {
	case types.TemplateAssignmentProbationary:
		assignment.ProbationStartedAt = now
		assignment.ShareCapPPM = transition.SharePPM
		assignment.MaxInFlight = transition.MaxInFlight
	case types.TemplateAssignmentActive:
		assignment.ActivatedAt = now
		assignment.ShareCapPPM = transition.SharePPM
		assignment.MaxInFlight = 0
	default:
		assignment.ShareCapPPM = transition.SharePPM
	}
	if err := s.repo.PutTemplateAssignment(assignment); err != nil {
		return err
	}

	tmpl, known := s.catalog.Get(assignment.TemplateID)
	if known {
		if state, err := s.repo.GetBackendSelectionState(
			assignment.MemberEthAddress, BackendID(assignment), tmpl.Capability, tmpl.OfferingID); err == nil {
			state.MaxShareCap = float64(transition.SharePPM) / 1_000_000
			// A suspended runner is excluded outright rather than given
			// a share of zero: zero is a weight, and a weight can be
			// rounded back up by a selector that refuses to starve
			// anyone.
			if transition.To == types.TemplateAssignmentSuspended {
				state.State = types.BackendSelectionStateExcluded
				state.ExclusionReason = transition.ReasonCode
			} else {
				state.State = types.BackendSelectionStateEligible
				state.ExclusionReason = ""
			}
			if err := s.repo.SaveBackendSelectionState(state); err != nil {
				return err
			}
		}
	}

	return s.repo.AppendAuditEvent(types.AuditEvent{
		Kind:         "ladder_transition",
		OccurredAt:   now,
		ResourceID:   transition.AssignmentID,
		ResourceType: "template_assignment",
		Details: map[string]any{
			"from": string(transition.From), "to": string(transition.To),
			"reason_code": transition.ReasonCode, "evidence": transition.Evidence,
			"share_ppm": transition.SharePPM,
		},
	})
}

func selectionKey(assignment types.TemplateAssignment, tmpl templates.Template) string {
	return strings.Join([]string{
		assignment.MemberEthAddress, BackendID(assignment), tmpl.Capability, tmpl.OfferingID,
	}, "|")
}

// hasClosedWindow reports whether the pool has completed a settlement
// round. Promotion waits on it because jobs served prove the runner
// answers, and only a closed round proves the pool was paid.
func hasClosedWindow(windows []types.SettlementWindow) bool {
	for _, window := range windows {
		switch window.Status {
		case types.SettlementWindowApproved, types.SettlementWindowPaid:
			return true
		}
	}
	return false
}
