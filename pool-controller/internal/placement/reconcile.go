package placement

import (
	"sort"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// Change is one difference between what the pool wants and what it has.
type Change struct {
	Kind       string                       `json:"kind"` // create | drain | role_change
	TemplateID string                       `json:"template_id"`
	HardwareID string                       `json:"hardware_unit_id"`
	Role       types.TemplateAssignmentRole `json:"role,omitempty"`
	Assignment types.TemplateAssignment     `json:"-"`
	Reason     string                       `json:"reason,omitempty"`
}

const (
	ChangeCreate     = "create"
	ChangeDrain      = "drain"
	ChangeRoleChange = "role_change"
)

// Reconcile turns a plan into the changes that would realise it.
//
// A template that should run and does not is created. One that runs and
// should not is DRAINED rather than deleted: the member's container is
// still serving, the broker is still dispatching to it, and pulling the
// record out from under that would strand in-flight work. Draining is
// the state the agent and the broker both already understand — the
// runner is marked draining in the attach document, the broker stops
// sending it new work, and the assignment retires once it is quiet
// (plan 0044 §3.4).
//
// An assignment already draining, suspended or retired is left alone;
// it is on its way out and re-creating it would undo that.
func Reconcile(existing []types.TemplateAssignment, decisions []Decision, now time.Time) []Change {
	type key struct{ hardware, template string }
	current := make(map[key]types.TemplateAssignment, len(existing))
	for _, assignment := range existing {
		current[key{assignment.HardwareUnitID, assignment.TemplateID}] = assignment
	}

	wanted := map[key]Placement{}
	var changes []Change
	for _, decision := range decisions {
		for _, placement := range decision.Placements {
			k := key{decision.HardwareUnitID, placement.TemplateID}
			wanted[k] = placement
			assignment, exists := current[k]
			if !exists {
				changes = append(changes, Change{
					Kind:       ChangeCreate,
					TemplateID: placement.TemplateID,
					HardwareID: decision.HardwareUnitID,
					Role:       placement.Role,
					Reason:     placement.Reason,
					Assignment: types.TemplateAssignment{
						ID:               decision.HardwareUnitID + "|" + placement.TemplateID,
						HardwareUnitID:   decision.HardwareUnitID,
						HostEnrollmentID: decision.HostEnrollmentID,
						MemberEthAddress: decision.MemberEthAddress,
						TemplateID:       placement.TemplateID,
						Role:             placement.Role,
						State:            types.TemplateAssignmentPending,
						CreatedAt:        now,
						UpdatedAt:        now,
					},
				})
				continue
			}
			if leaving(assignment.State) {
				continue
			}
			// A primary demoted to secondary is a real change — it is
			// what the card is allowed to spend itself on — but it does
			// not need a new certification, so it is not a create.
			if assignment.Role != placement.Role {
				changes = append(changes, Change{
					Kind:       ChangeRoleChange,
					TemplateID: placement.TemplateID,
					HardwareID: decision.HardwareUnitID,
					Role:       placement.Role,
					Assignment: assignment,
				})
			}
		}
	}

	for k, assignment := range current {
		if _, keep := wanted[k]; keep {
			continue
		}
		if leaving(assignment.State) {
			continue
		}
		changes = append(changes, Change{
			Kind:       ChangeDrain,
			TemplateID: assignment.TemplateID,
			HardwareID: assignment.HardwareUnitID,
			Assignment: assignment,
			Reason:     "no longer in the desired plan",
		})
	}

	// Deterministic order: this list is logged, shown to operators, and
	// compared between runs.
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].HardwareID != changes[j].HardwareID {
			return changes[i].HardwareID < changes[j].HardwareID
		}
		if changes[i].TemplateID != changes[j].TemplateID {
			return changes[i].TemplateID < changes[j].TemplateID
		}
		return changes[i].Kind < changes[j].Kind
	})
	return changes
}

// leaving reports an assignment already on its way out.
func leaving(state types.TemplateAssignmentState) bool {
	switch state {
	case types.TemplateAssignmentDraining,
		types.TemplateAssignmentSuspended,
		types.TemplateAssignmentRetired:
		return true
	default:
		return false
	}
}
