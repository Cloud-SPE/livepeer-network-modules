package admin

import (
	"net/http"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/ladder"
)

func registerLadderRoutes(mux *http.ServeMux, deps Deps, auth func(http.HandlerFunc) http.HandlerFunc) {
	// Running the ladder is idempotent — it seeds what is missing and
	// moves only what the evidence justifies — so an operator may run
	// it by hand without waiting for the worker's tick.
	registerLadderStateRoute(mux, deps, auth)
	mux.HandleFunc("POST /admin/v1/ladder/run", auth(func(w http.ResponseWriter, _ *http.Request) {
		if deps.Ladder == nil {
			http.Error(w, "ladder is not configured", http.StatusInternalServerError)
			return
		}
		summary, err := deps.Ladder.RunOnce()
		writeAdminJSON(w, summary, err)
	}))
}

// The ladder's current standing, read without running it.
//
// Running the ladder to see where things stand would mean the only way
// to look was to act — an operator opening a page should not move a
// member's placement. The transitions themselves live in the audit
// trail; this is where each placement sits now and what share it
// carries.
func registerLadderStateRoute(mux *http.ServeMux, deps Deps, auth func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /admin/v1/ladder/state", auth(func(w http.ResponseWriter, _ *http.Request) {
		assignments, err := deps.Repo.ListTemplateAssignments()
		if err != nil {
			writeAdminJSON(w, nil, err)
			return
		}
		reasons := deps.latestLadderTransitions()
		out := make([]ladderStateView, 0, len(assignments))
		for _, assignment := range assignments {
			view := ladderStateView{
				AssignmentID:     assignment.ID,
				HardwareUnitID:   assignment.HardwareUnitID,
				MemberEthAddress: assignment.MemberEthAddress,
				TemplateID:       assignment.TemplateID,
				State:            string(assignment.State),
				Role:             string(assignment.Role),
				SharePPM:         assignment.ShareCapPPM,
				MaxInFlight:      assignment.MaxInFlight,
				UpdatedAt:        assignment.UpdatedAt,
			}
			if reason, ok := reasons[assignment.ID]; ok {
				view.ReasonCode = reason.code
				view.Evidence = reason.evidence
			}
			out = append(out, view)
		}
		writeAdminJSON(w, struct {
			Placements []ladderStateView `json:"placements"`
		}{Placements: out}, nil)
	}))
}

type ladderStateView struct {
	AssignmentID     string    `json:"assignment_id"`
	HardwareUnitID   string    `json:"hardware_unit_id"`
	MemberEthAddress string    `json:"member_eth_address"`
	TemplateID       string    `json:"template_id"`
	State            string    `json:"state"`
	Role             string    `json:"role,omitempty"`
	ReasonCode       string    `json:"reason_code,omitempty"`
	Evidence         string    `json:"evidence,omitempty"`
	SharePPM         uint64    `json:"share_ppm,omitempty"`
	MaxInFlight      int       `json:"max_in_flight,omitempty"`
	UpdatedAt        time.Time `json:"updated_at,omitempty"`
}

type ladderTransitionReason struct{ code, evidence string }

// latestLadderTransitions reads the most recent reason per placement
// out of the audit trail, so the console and the member portal quote
// the same sentence rather than two descriptions of one decision.
func (d Deps) latestLadderTransitions() map[string]ladderTransitionReason {
	out := map[string]ladderTransitionReason{}
	events, err := d.Repo.ListAuditEvents()
	if err != nil {
		return out
	}
	for _, event := range events {
		if event.Kind != "ladder_transition" || event.ResourceID == "" {
			continue
		}
		code, _ := event.Details["reason_code"].(string)
		evidence, _ := event.Details["evidence"].(string)
		out[event.ResourceID] = ladderTransitionReason{code: code, evidence: evidence}
	}
	return out
}

// LadderRunner is the ladder as the admin surface needs it.
type LadderRunner interface {
	RunOnce() (ladder.Summary, error)
}

var _ LadderRunner = (*ladder.Service)(nil)
