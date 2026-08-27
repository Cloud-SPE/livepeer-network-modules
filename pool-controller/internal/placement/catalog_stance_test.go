package placement

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// The catalog and the engine are two halves of one policy: 0040 §4.4
// says what a card of each class should end up running, the templates
// encode it in requirements and stacking, and the engine applies it.
// Each half can be edited without the other and nothing else would
// notice — a VRAM floor raised above what a 3090 reports, or a
// secondary_on entry dropped, would quietly idle real hardware. This
// test is the only place the two are compared against the plan.
func TestShippedCatalogProducesPlannedStance(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "templates")
	if _, err := os.Stat(dir); err != nil {
		// The catalog sits at the repo root, outside this module. A
		// checkout of pool-controller alone is still a valid thing to
		// test; it just has no catalog to check.
		t.Skip("no repo-root templates/ directory alongside this module")
	}
	catalog, err := templates.Load(dir)
	if err != nil {
		t.Fatalf("templates.Load(%s) = %v", dir, err)
	}
	all := catalog.All()
	if len(all) == 0 {
		t.Fatalf("shipped catalog is empty")
	}
	// A pool that enabled everything is the strongest form of the
	// question: with every template competing, does each class still
	// land where the plan says?
	overrides := make([]types.TemplateOverride, 0, len(all))
	for _, tmpl := range all {
		overrides = append(overrides, types.TemplateOverride{TemplateID: tmpl.ID, Enabled: true})
	}

	// VRAM as the cards actually report it, since the catalog's floors
	// are set just under those figures on purpose.
	decisions := Plan(Input{
		Hardware: []types.HardwareUnit{
			gpu("gpu-1080", model1080, 8*gib),
			gpu("gpu-3090", model3090, 24*gib),
			gpu("gpu-4090", model4090, 24*gib),
		},
		Templates: all,
		Overrides: overrides,
	})

	byUnit := map[string]Decision{}
	for _, decision := range decisions {
		byUnit[decision.HardwareUnitID] = decision
	}

	// GTX 1080: one template, and it has to be transcode — everything
	// else in the catalog requires a 3090 or better.
	gtx := byUnit["gpu-1080"]
	if gtx.GPUClass != ClassGTX1080 {
		t.Fatalf("gpu-1080 class = %q, want %q", gtx.GPUClass, ClassGTX1080)
	}
	assertPlacements(t, "gtx-1080", gtx, []string{"video-transcode-abr/primary/placed_primary"})

	// RTX 3090: one template. Embeddings outranks audio and transcode,
	// and the class stance forbids a second.
	rtx3090 := byUnit["gpu-3090"]
	if rtx3090.GPUClass != ClassRTX3090 {
		t.Fatalf("gpu-3090 class = %q, want %q", rtx3090.GPUClass, ClassRTX3090)
	}
	assertPlacements(t, "rtx-3090", rtx3090, []string{"openai-embeddings-nomic-embed-text/primary/placed_primary"})

	// RTX 4090: a primary plus the audio secondary — the one stacking
	// combination 0040 §4.4 names.
	rtx4090 := byUnit["gpu-4090"]
	if rtx4090.GPUClass != ClassRTX4090 {
		t.Fatalf("gpu-4090 class = %q, want %q", rtx4090.GPUClass, ClassRTX4090)
	}
	assertPlacements(t, "rtx-4090", rtx4090, []string{
		"openai-chat-gpt-oss-20b/primary/placed_primary",
		"openai-audio-transcriptions-whisper-large-v3/secondary/placed_secondary",
	})
	// Named by capability as well as by id: the plan's stance is about
	// what runs on the card, and an id can be renamed.
	secondary, ok := catalog.Get(rtx4090.Placements[1].TemplateID)
	if !ok {
		t.Fatalf("secondary %q is not in the catalog", rtx4090.Placements[1].TemplateID)
	}
	if secondary.Capability != "openai:audio-transcriptions" {
		t.Errorf("4090 secondary is %q, want the audio-transcription family 0040 §4.4 allows as a rider",
			secondary.Capability)
	}
}

func assertPlacements(t *testing.T, class string, decision Decision, want []string) {
	t.Helper()
	got := placementStrings(decision)
	if len(got) != len(want) {
		t.Errorf("%s runs %v, want %v (0040 §4.4)", class, got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s placement[%d] = %q, want %q", class, i, got[i], want[i])
		}
	}
}
