package placement

import (
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func validateUnit(model string, vram uint64, state types.HardwareUnitState) types.HardwareUnit {
	if state == "" {
		state = types.HardwareUnitActive
	}
	return types.HardwareUnit{ID: "gpu-1", GPUModel: model, VRAMBytes: vram, State: state}
}

// Validate and Plan must agree. An override checked by a different rule
// than the planner's is the failure mode this whole function exists to
// prevent, so the agreement is asserted directly rather than assumed
// from the shared call.
func TestValidateAgreesWithWhatThePlannerWouldPlace(t *testing.T) {
	tmpl := templates.Template{
		ID: "t-chat", Priority: 30,
		Requirements: templates.Requirements{GPUClasses: []string{ClassRTX4090}},
		Stacking:     templates.Stacking{Primary: true},
	}
	for _, tc := range []struct {
		name  string
		unit  types.HardwareUnit
		plans bool
	}{
		{"a card the planner places", validateUnit("NVIDIA GeForce RTX 4090", 24<<30, ""), true},
		{"a card the planner rejects", validateUnit("NVIDIA GeForce GTX 1080", 8<<30, ""), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decisions := Plan(Input{
				Templates: []templates.Template{tmpl},
				Overrides: []types.TemplateOverride{{TemplateID: tmpl.ID, Enabled: true}},
				Hardware:  []types.HardwareUnit{tc.unit},
			})
			planned := len(decisions) == 1 && len(decisions[0].Placements) == 1
			err := Validate(tmpl, tc.unit, types.TemplateAssignmentPrimary)
			if planned != tc.plans {
				t.Fatalf("setup: planner placed = %v, want %v", planned, tc.plans)
			}
			if planned != (err == nil) {
				t.Fatalf("the planner %s this pairing but Validate said %v — an override checked "+
					"by a different rule than the planner is worse than no check",
					map[bool]string{true: "placed", false: "refused"}[planned], err)
			}
		})
	}
}

// The VRAM floor is a requirement like any other.
func TestValidateEnforcesTheVRAMFloor(t *testing.T) {
	tmpl := templates.Template{
		ID:       "t-big",
		Stacking: templates.Stacking{Primary: true},
		Requirements: templates.Requirements{
			GPUClasses: []string{ClassRTX4090}, GPUVRAMMinBytes: 32 << 30,
		},
	}
	err := Validate(tmpl, validateUnit("NVIDIA GeForce RTX 4090", 24<<30, ""), types.TemplateAssignmentPrimary)
	if err == nil {
		t.Fatal("a 24GiB card was accepted for a template needing 32GiB")
	}
	if !strings.Contains(err.Error(), "24GiB") {
		t.Errorf("error %q does not say what the card actually has", err)
	}
}

// Validate deliberately stops at the hardware gate: stacking limits and
// priority are pool preference, and overriding a preference is what an
// override is for. Only a card that physically cannot run the workload
// is refused.
func TestValidateDoesNotEnforcePoolPreferences(t *testing.T) {
	tmpl := templates.Template{
		ID: "t-audio", Priority: 1,
		Requirements: templates.Requirements{GPUClasses: []string{ClassRTX4090}},
		Stacking:     templates.Stacking{Primary: true, SecondaryOn: []string{ClassRTX4090}},
	}
	unit := validateUnit("NVIDIA GeForce RTX 4090", 24<<30, "")
	if err := Validate(tmpl, unit, types.TemplateAssignmentSecondary); err != nil {
		t.Fatalf("a stackable template on a compatible card was refused: %v", err)
	}
}
