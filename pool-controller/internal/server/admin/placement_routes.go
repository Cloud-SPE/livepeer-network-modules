package admin

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/placement"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// Placement is deterministic policy over declared facts, so the plan is
// worth showing before it is applied — an operator asking "what would
// this do" gets a real answer, and an operator asking "why is that card
// idle" gets the reason codes rather than a shrug.

type placementPlanResponse struct {
	GeneratedAt time.Time            `json:"generated_at"`
	Decisions   []placement.Decision `json:"decisions"`
	Changes     []placement.Change   `json:"changes"`
	// NotEnabled names catalog templates this pool has not switched on.
	// It is the likeliest reason a workload is running nowhere, and it
	// is pool-wide rather than a property of any one card.
	NotEnabled []string `json:"not_enabled,omitempty"`
}

func (d Deps) placementInput() (placement.Input, error) {
	hardware, err := d.Repo.ListHardwareUnits()
	if err != nil {
		return placement.Input{}, err
	}
	overrides, err := d.Repo.ListTemplateOverrides()
	if err != nil {
		return placement.Input{}, err
	}
	optOuts, err := d.Repo.ListMemberTemplateOptOuts()
	if err != nil {
		return placement.Input{}, err
	}
	return placement.Input{
		Hardware:  hardware,
		Templates: d.Catalog.All(),
		Overrides: overrides,
		OptOuts:   optOuts,
		Stances:   d.Stances,
	}, nil
}

func (d Deps) placementPlan(now time.Time) (placementPlanResponse, error) {
	in, err := d.placementInput()
	if err != nil {
		return placementPlanResponse{}, err
	}
	existing, err := d.Repo.ListTemplateAssignments()
	if err != nil {
		return placementPlanResponse{}, err
	}
	decisions := placement.Plan(in)
	return placementPlanResponse{
		GeneratedAt: now,
		Decisions:   decisions,
		Changes:     placement.Reconcile(existing, decisions, now),
		NotEnabled:  placement.NotEnabled(in.Templates, in.Overrides),
	}, nil
}

func registerPlacementRoutes(mux *http.ServeMux, deps Deps, auth func(http.HandlerFunc) http.HandlerFunc) {
	// Read-only: what the policy wants, and what would change.
	mux.HandleFunc("GET /admin/v1/placement-plan", auth(func(w http.ResponseWriter, _ *http.Request) {
		plan, err := deps.placementPlan(time.Now().UTC())
		writeAdminJSON(w, plan, err)
	}))

	// Apply. Creating an assignment is safe; withdrawing one is not, so
	// this drains rather than deletes and leaves the retirement to the
	// drain completing.
	mux.HandleFunc("POST /admin/v1/placement-plan/apply", auth(func(w http.ResponseWriter, _ *http.Request) {
		now := time.Now().UTC()
		plan, err := deps.placementPlan(now)
		if err != nil {
			writeAdminJSON(w, nil, err)
			return
		}
		applied := make([]placement.Change, 0, len(plan.Changes))
		for _, change := range plan.Changes {
			assignment := change.Assignment
			switch change.Kind {
			case placement.ChangeCreate:
			case placement.ChangeRoleChange:
				assignment.Role = change.Role
				assignment.UpdatedAt = now
			case placement.ChangeDrain:
				assignment.State = types.TemplateAssignmentDraining
				assignment.DrainingSince = now
				assignment.UpdatedAt = now
			default:
				continue
			}
			if err := deps.Repo.PutTemplateAssignment(assignment); err != nil {
				writeAdminJSON(w, nil, err)
				return
			}
			applied = append(applied, change)
		}
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "placement_plan_applied",
			OccurredAt:   now,
			ResourceType: "template_assignment",
			Details:      map[string]any{"changes": len(applied)},
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(struct {
			Status  string             `json:"status"`
			Applied []placement.Change `json:"applied"`
		}{Status: "applied", Applied: applied})
	}))
}
