package admin

import (
	"fmt"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/placement"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// validateTemplateAssignment checks an operator-written assignment
// against the same gates the planner applies.
//
// The route used to accept anything the JSON decoder could parse. That
// was tolerable while a legacy path rejected incompatible pairings
// downstream; deleting that path made the gap visible. The cost of the
// gap is not an error — it is silence: an assignment naming a template
// that is not in the catalog renders no service, and one naming a card
// that cannot run the workload renders a container that never starts.
// Both surface far from the request that caused them.
//
// It also fills in what the assignment can only get from the hardware
// unit — the host and the member — so an operator does not have to
// restate three ids consistently and cannot get them wrong.
func validateTemplateAssignment(deps Deps, item *types.TemplateAssignment) error {
	templateID := strings.TrimSpace(item.TemplateID)
	if templateID == "" {
		return fmt.Errorf("template_id is required")
	}
	unitID := strings.TrimSpace(item.HardwareUnitID)
	if unitID == "" {
		return fmt.Errorf("hardware_unit_id is required")
	}
	if deps.Catalog == nil {
		return fmt.Errorf("no template catalog is loaded, so no assignment can be validated")
	}
	tmpl, ok := deps.Catalog.Get(templateID)
	if !ok {
		return fmt.Errorf("no template %s in the catalog", templateID)
	}
	unit, err := deps.Repo.GetHardwareUnit(unitID)
	if err != nil {
		return fmt.Errorf("no hardware unit %s: %w", unitID, err)
	}
	if err := placement.Validate(tmpl, unit, item.Role); err != nil {
		return err
	}
	// Derived, never trusted from the body: an assignment whose host or
	// member disagreed with the card's would send this workload's
	// desired state to the wrong machine and its earnings to the wrong
	// wallet.
	item.HostEnrollmentID = unit.EnrollmentID
	item.MemberEthAddress = unit.MemberEthAddress
	if strings.TrimSpace(item.ID) == "" {
		// The planner's id, so a hand-written assignment and a planned
		// one for the same pair are the same record rather than two
		// that quietly double the card's work.
		item.ID = unit.ID + "|" + tmpl.ID
	}
	return nil
}

// refresh re-renders what the controller publishes after a write, and
// tolerates not having been given a way to.
//
// Production always supplies one. Test and embedded callers construct a
// Deps with only the fields their routes touch, and a nil callback there
// took the whole process down on an operator's request — a panic in a
// handler goroutine, so the operator saw a dropped connection rather
// than an error. A missing refresh is at worst a stale rendered view;
// it is not worth a crash.
func (d Deps) refresh(reason string) error {
	if d.RefreshRendered == nil {
		return nil
	}
	return d.RefreshRendered(reason)
}
