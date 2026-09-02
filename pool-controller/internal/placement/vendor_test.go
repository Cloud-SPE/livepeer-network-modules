package placement

import (
	"testing"

	gpuv "github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/gpu"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// Intel's driver strings carry trademark noise; the class must still
// come out of them.
func TestClassOfReadsIntelCards(t *testing.T) {
	cases := map[string]string{
		"Intel(R) Arc(TM) A770 Graphics":    ClassArcA770,
		"Intel Arc B580":                    ClassArcB580,
		"Intel(R) Data Center GPU Flex 170": ClassFlex170,
		"Intel(R) UHD Graphics 770":         ClassUnknown,
	}
	for model, want := range cases {
		if got := ClassOf(model); got != want {
			t.Errorf("ClassOf(%q) = %q, want %q", model, got, want)
		}
	}
}

// Two packages hold the class vocabulary: this one matches driver
// strings to classes, and gpu maps classes to vendors so the catalog
// validator can check a template without importing placement. They
// have to agree, or a class placement knows is one the validator
// cannot reason about — and the reverse.
func TestEveryPlacementClassHasAVendorAndTheReverse(t *testing.T) {
	placementClasses := map[string]bool{}
	for _, mp := range modelPatterns {
		placementClasses[mp.class] = true
	}
	// CPU tiers come from core counts, not model strings (plan 0047).
	for _, cores := range []int{8, 16, 32, 64} {
		placementClasses[CPUClassOf(cores)] = true
	}
	for class := range placementClasses {
		if gpuv.VendorOfClass(class) == "" {
			t.Errorf("placement knows class %q but gpu has no vendor for it", class)
		}
	}
	for _, class := range gpuv.Classes() {
		if !placementClasses[class] {
			t.Errorf("gpu lists class %q but placement has no pattern that produces it", class)
		}
	}
}

func vendorTemplate(images map[string]string) templates.Template {
	return templates.Template{
		ID: "t", Priority: 10,
		Requirements:  templates.Requirements{GPUClasses: []string{ClassRTX4090, ClassArcA770}},
		Stacking:      templates.Stacking{Primary: true},
		RunnerCompose: templates.RunnerCompose{Image: images},
	}
}

func vendorUnit(model string) types.HardwareUnit {
	return types.HardwareUnit{ID: "gpu-1", GPUModel: model, VRAMBytes: 16 << 30, State: types.HardwareUnitActive}
}

// The hardware gate's last axis. A card whose vendor the template has no
// build for is refused with a named reason, never placed to fail at
// compose up on a member's host.
func TestPlacementRefusesACardWhoseVendorHasNoImage(t *testing.T) {
	tmpl := vendorTemplate(map[string]string{gpuv.VendorNVIDIA: "x/y:1"})
	reason, detail := requirementsFail(tmpl, vendorUnit("Intel(R) Arc(TM) A770 Graphics"), ClassArcA770)
	if reason != ReasonNoImageForVendor {
		t.Fatalf("reason = %q (%s), want %s", reason, detail, ReasonNoImageForVendor)
	}
	// Cover the vendor and the same card places.
	tmpl = vendorTemplate(map[string]string{gpuv.VendorNVIDIA: "x/y:1", gpuv.VendorIntel: "x/y-intel:1"})
	if reason, detail := requirementsFail(tmpl, vendorUnit("Intel(R) Arc(TM) A770 Graphics"), ClassArcA770); reason != "" {
		t.Fatalf("a covered Intel card was refused: %s (%s)", reason, detail)
	}
}

// A card that names no vendor the pool knows cannot be given an image,
// so a template that ships one refuses it — but a template that ships
// no image at all is not gated, because unresolved is not wrong.
func TestPlacementVendorGateOnlyAppliesWhenAnImageShips(t *testing.T) {
	odd := vendorUnit("Mystery Accelerator 9000")
	with := vendorTemplate(map[string]string{gpuv.VendorNVIDIA: "x/y:1"})
	with.Requirements = templates.Requirements{} // no class gate, isolate the vendor one
	if reason, _ := requirementsFail(with, odd, ClassUnknown); reason != ReasonNoImageForVendor {
		t.Fatalf("an unknown-vendor card was accepted for a template with an image: %q", reason)
	}
	without := vendorTemplate(nil)
	without.Requirements = templates.Requirements{}
	if reason, _ := requirementsFail(without, odd, ClassUnknown); reason != "" {
		t.Fatalf("an image-less template was vendor-gated: %q", reason)
	}
}
