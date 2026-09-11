package placement

import (
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// Validate reports why the planner would not have made this assignment.
//
// The operator can write an assignment by hand — placement is policy,
// and policy needs an override. What an override must not be is a
// different rule set: an assignment the planner would refuse produces a
// container pinned to a card that cannot run it, which surfaces as a
// pull that never starts or a certification that fails for no stated
// reason, hours later and nowhere near the request that caused it.
//
// So the escape hatch is checked against the same gates Plan applies,
// by calling them rather than restating them. What it deliberately does
// NOT check is the policy layer above those gates — stacking limits per
// class, priority order, member opt-outs. Those are the pool's own
// preferences, and overriding a preference is the entire point of an
// override. A card that physically cannot run the workload is not a
// preference.
func Validate(tmpl templates.Template, unit types.HardwareUnit, role types.TemplateAssignmentRole) error {
	if !placeable(unit.State) {
		return fmt.Errorf("hardware unit %s is %s and takes no new work", unit.ID, unit.State)
	}
	class := ClassOf(unit.GPUModel)
	if reason, detail := requirementsFail(tmpl, unit, class); reason != "" {
		return fmt.Errorf("%s does not meet %s's requirements (%s: %s)",
			describeUnit(unit), tmpl.ID, reason, detail)
	}
	switch role {
	case types.TemplateAssignmentPrimary, "":
		if !tmpl.Stacking.Primary {
			return fmt.Errorf("%s does not declare stacking.primary, so it cannot hold a card's primary slot", tmpl.ID)
		}
	case types.TemplateAssignmentSecondary:
		if !stackableOn(tmpl, class) {
			return fmt.Errorf("%s does not declare %s in stacking.secondary_on, so it cannot ride alongside another workload there",
				tmpl.ID, displayClass(class))
		}
	default:
		return fmt.Errorf("role %q is neither %s nor %s", role,
			types.TemplateAssignmentPrimary, types.TemplateAssignmentSecondary)
	}
	return nil
}

// describeUnit names a card the way an operator would recognise it.
func describeUnit(unit types.HardwareUnit) string {
	if unit.GPUModel == "" {
		return "hardware unit " + unit.ID
	}
	return unit.GPUModel + " (" + unit.ID + ")"
}
