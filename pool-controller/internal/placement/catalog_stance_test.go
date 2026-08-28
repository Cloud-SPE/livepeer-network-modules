package placement

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// The catalog and the engine are two halves of one policy: plan 0040
// §4.4 says how much a card of each class should run, the templates
// encode it in requirements and stacking, and the engine applies it.
// Either half can be edited without the other noticing — a VRAM floor
// raised above what a 3090 reports, or a secondary_on entry dropped,
// would quietly idle real hardware.
//
// What this asserts is the STANCE, not the cast. An earlier version
// pinned exact template ids, which made it a test of which products the
// pool happened to sell that week: it broke the moment the catalog was
// rebuilt from the operator's real configuration, and it would have
// broken again on the next price change. The rule worth defending is
// that older consumer cards run one workload and 4090/5090-class cards
// run a primary plus at most one low-footprint rider — which stays true
// whoever that rider turns out to be.
func TestShippedCatalogProducesPlannedStance(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "templates")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no repo-root templates/ directory alongside this module")
	}
	catalog, err := templates.Load(dir)
	if err != nil {
		t.Fatalf("Load(%s) = %v", dir, err)
	}
	all := catalog.All()
	if len(all) == 0 {
		t.Fatal("the shipped catalog is empty")
	}
	overrides := make([]types.TemplateOverride, 0, len(all))
	for _, tmpl := range all {
		overrides = append(overrides, types.TemplateOverride{TemplateID: tmpl.ID, Enabled: true})
	}

	// One card of each class the pool has a stance on, at a memory size
	// that class really ships with.
	cards := []struct {
		class string
		model string
		vram  uint64
	}{
		{ClassGTX1080, "NVIDIA GeForce GTX 1080", 8},
		{ClassRTX2080, "NVIDIA GeForce RTX 2080 Ti", 11},
		{ClassRTX3090, "NVIDIA GeForce RTX 3090", 24},
		{ClassRTX4090, "NVIDIA GeForce RTX 4090", 24},
		{ClassRTX5090, "NVIDIA GeForce RTX 5090", 32},
	}
	hardware := make([]types.HardwareUnit, 0, len(cards))
	for _, card := range cards {
		hardware = append(hardware, types.HardwareUnit{
			ID: "gpu-" + card.class, MemberEthAddress: "0xa",
			GPUModel: card.model, VRAMBytes: card.vram << 30, State: types.HardwareUnitOnline,
		})
	}

	byID := make(map[string]templates.Template, len(all))
	for _, tmpl := range all {
		byID[tmpl.ID] = tmpl
	}
	decisions := Plan(Input{Hardware: hardware, Templates: all, Overrides: overrides})
	for _, decision := range decisions {
		limit := MaxTemplatesFor(decision.GPUClass, nil)
		if len(decision.Placements) > limit {
			t.Fatalf("%s got %d templates, and the class stance allows %d",
				decision.GPUClass, len(decision.Placements), limit)
		}
		// Every card the pool has a stance on should be earning. One
		// running nothing is either a requirements block that excludes
		// real hardware or a catalog with a hole in it, and both are
		// worth failing over.
		if len(decision.Placements) == 0 {
			t.Errorf("%s is placed nothing; rejections: %+v", decision.GPUClass, decision.Rejections)
			continue
		}
		if decision.Placements[0].Role != types.TemplateAssignmentPrimary {
			t.Errorf("%s: first placement is %s, want the primary",
				decision.GPUClass, decision.Placements[0].Role)
		}
		for _, placement := range decision.Placements[1:] {
			if placement.Role != types.TemplateAssignmentSecondary {
				t.Errorf("%s: %s is a second primary", decision.GPUClass, placement.TemplateID)
			}
			// A rider must have said it can ride on this class. The
			// engine checks it; asserting it here is what catches a
			// catalog edit that drops the declaration.
			rider := byID[placement.TemplateID]
			if !containsFold(rider.Stacking.SecondaryOn, decision.GPUClass) {
				t.Errorf("%s carries rider %s, which does not declare that class in stacking.secondary_on",
					decision.GPUClass, placement.TemplateID)
			}
			// And a rider has to be bounded, or "low-footprint" is a
			// word in a plan rather than a property of the offering.
			if rider.Capacity.MaxInFlight <= 0 {
				t.Errorf("rider %s has no capacity.max_in_flight; a workload sharing a card "+
					"with a primary has to be bounded", placement.TemplateID)
			}
		}
	}

	// The older classes run one workload and nothing else (0040 §4.4).
	for _, decision := range decisions {
		switch decision.GPUClass {
		case ClassGTX1080, ClassRTX2080, ClassRTX3090:
			if len(decision.Placements) != 1 {
				t.Errorf("%s runs %d templates; §4.4 gives this class one",
					decision.GPUClass, len(decision.Placements))
			}
		}
	}
}

// Every template that offers itself as a rider has to be placeable as
// one. A secondary_on naming a class the template's own requirements
// exclude is a contradiction the catalog can hold quietly.
func TestShippedRidersCanActuallyRideWhereTheySay(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "templates")
	if _, err := os.Stat(dir); err != nil {
		t.Skip("no repo-root templates/ directory alongside this module")
	}
	catalog, err := templates.Load(dir)
	if err != nil {
		t.Fatalf("Load(%s) = %v", dir, err)
	}
	for _, tmpl := range catalog.All() {
		for _, class := range tmpl.Stacking.SecondaryOn {
			if len(tmpl.Requirements.GPUClasses) == 0 {
				continue // no constraint on that axis
			}
			if !containsFold(tmpl.Requirements.GPUClasses, class) {
				t.Errorf("%s offers to ride on %s but its requirements.gpu_classes exclude it: %v",
					tmpl.ID, class, tmpl.Requirements.GPUClasses)
			}
		}
	}
}
