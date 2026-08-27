package placement

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

const (
	testMember  = "0x1111111111111111111111111111111111111111"
	otherMember = "0x2222222222222222222222222222222222222222"

	model4090 = "NVIDIA GeForce RTX 4090"
	model3090 = "NVIDIA GeForce RTX 3090"
	model1080 = "NVIDIA GeForce GTX 1080"
	// A card the pool has no policy for, used to prove that an
	// unconstrained template still lands on it.
	modelUnknown = "NVIDIA GeForce RTX 4060"

	gib = uint64(1) << 30
)

func gpu(id, model string, vram uint64) types.HardwareUnit {
	return types.HardwareUnit{
		ID:               id,
		EnrollmentID:     "host-1",
		MemberEthAddress: testMember,
		GPUUUID:          "GPU-" + id,
		GPUModel:         model,
		VRAMBytes:        vram,
		State:            types.HardwareUnitActive,
	}
}

// tpl is a minimally valid catalog template: a primary that constrains
// nothing. Cases narrow it down to whatever they are testing.
func tpl(id string, priority int) templates.Template {
	return templates.Template{
		ID:           id,
		Capability:   "openai:chat-completions",
		OfferingID:   id,
		Protocol:     "paid-job/v1",
		PriceDefault: templates.Price{AmountWei: "1"},
		Priority:     priority,
		Stacking:     templates.Stacking{Primary: true},
	}
}

func withRequirements(t templates.Template, req templates.Requirements) templates.Template {
	t.Requirements = req
	return t
}

func withStacking(t templates.Template, s templates.Stacking) templates.Template {
	t.Stacking = s
	return t
}

func placementStrings(d Decision) []string {
	out := make([]string, 0, len(d.Placements))
	for _, p := range d.Placements {
		out = append(out, fmt.Sprintf("%s/%s/%s", p.TemplateID, p.Role, p.Reason))
	}
	return out
}

func rejectionStrings(d Decision) []string {
	out := make([]string, 0, len(d.Rejections))
	for _, r := range d.Rejections {
		out = append(out, fmt.Sprintf("%s/%s", r.TemplateID, r.Reason))
	}
	return out
}

type planCase struct {
	name string
	unit types.HardwareUnit
	// enabled/disabled build the pool's overrides. A template in
	// neither list has no override at all, which is a different fact
	// from being disabled.
	catalog  []templates.Template
	enabled  []string
	disabled []string
	optOuts  []types.MemberTemplateOptOut
	stances  map[string]int

	wantClass string
	// "<template>/<role>/<reason>" and "<template>/<reason>", in the
	// order the engine reports them.
	wantPlacements []string
	wantRejections []string
	// wantDetails is checked per rejected template where the detail is
	// the answer a member actually reads.
	wantDetails map[string]string
}

// The reason codes are asserted alongside the placements because they
// are the answer a member gets to "why is my card idle" — a right
// placement reported under the wrong reason is still a support ticket.
func TestPlan(t *testing.T) {
	tests := []planCase{
		{
			// enabledTemplates drops it before any GPU is considered,
			// so there is no rejection either: a template the pool
			// never adopted is not a decision about this card.
			name:           "template with no override is not placed",
			unit:           gpu("gpu-1", model4090, 24*gib),
			catalog:        []templates.Template{tpl("t-chat", 30)},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{},
			wantRejections: []string{},
		},
		{
			name:           "explicitly disabled template is not placed",
			unit:           gpu("gpu-1", model4090, 24*gib),
			catalog:        []templates.Template{tpl("t-chat", 30)},
			disabled:       []string{"t-chat"},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{},
			wantRejections: []string{},
		},
		{
			name: "priority decides which template claims the card",
			unit: gpu("gpu-1", model4090, 24*gib),
			catalog: []templates.Template{
				tpl("t-transcode", 10),
				tpl("t-chat", 30),
			},
			enabled:        []string{"t-transcode", "t-chat"},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{"t-chat/primary/placed_primary"},
			wantRejections: []string{"t-transcode/not_stackable_on_this_class"},
		},
		{
			// Catalog files load in directory order; the plan must not.
			name: "equal priority breaks on template id",
			unit: gpu("gpu-1", model4090, 24*gib),
			catalog: []templates.Template{
				tpl("t-bbb", 20),
				tpl("t-aaa", 20),
			},
			enabled:        []string{"t-aaa", "t-bbb"},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{"t-aaa/primary/placed_primary"},
			wantRejections: []string{"t-bbb/not_stackable_on_this_class"},
		},
		{
			// The rule that matters most: an empty requirement list is
			// no constraint on that axis. Read backwards it would mean
			// "matches nothing", and every template would be either
			// universal or unplaceable.
			name:           "empty gpu_classes is no constraint, even on an unclassified card",
			unit:           gpu("gpu-1", modelUnknown, 8*gib),
			catalog:        []templates.Template{tpl("t-any", 10)},
			enabled:        []string{"t-any"},
			wantClass:      ClassUnknown,
			wantPlacements: []string{"t-any/primary/placed_primary"},
			wantRejections: []string{},
		},
		{
			name: "empty gpu_models is no constraint when gpu_classes matches",
			unit: gpu("gpu-1", model4090, 24*gib),
			catalog: []templates.Template{withRequirements(tpl("t-chat", 30),
				templates.Requirements{GPUClasses: []string{ClassRTX4090, ClassRTX5090}})},
			enabled:        []string{"t-chat"},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{"t-chat/primary/placed_primary"},
			wantRejections: []string{},
		},
		{
			name: "a non-empty gpu_classes list must contain this card's class",
			unit: gpu("gpu-1", model4090, 24*gib),
			catalog: []templates.Template{withRequirements(tpl("t-old", 10),
				templates.Requirements{GPUClasses: []string{ClassGTX1080, ClassRTX2080}})},
			enabled:        []string{"t-old"},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{},
			wantRejections: []string{"t-old/gpu_class_not_allowed"},
			wantDetails:    map[string]string{"t-old": ClassRTX4090},
		},
		{
			// A class-constrained template on a card the pool cannot
			// class is rejected for a distinct reason: the answer is
			// "we don't know this GPU", not "your GPU is wrong".
			name: "a class-constrained template rejects an unclassified card by name",
			unit: gpu("gpu-1", "NVIDIA GeForce RTX 4090 Laptop GPU", 16*gib),
			catalog: []templates.Template{withRequirements(tpl("t-chat", 30),
				templates.Requirements{GPUClasses: []string{ClassRTX4090}})},
			enabled:        []string{"t-chat"},
			wantClass:      ClassUnknown,
			wantPlacements: []string{},
			wantRejections: []string{"t-chat/gpu_class_unknown"},
			wantDetails:    map[string]string{"t-chat": "NVIDIA GeForce RTX 4090 Laptop GPU"},
		},
		{
			name: "a non-empty gpu_models list must contain this card's driver string",
			unit: gpu("gpu-1", model3090, 24*gib),
			catalog: []templates.Template{withRequirements(tpl("t-pinned", 10),
				templates.Requirements{GPUModels: []string{model4090}})},
			enabled:        []string{"t-pinned"},
			wantClass:      ClassRTX3090,
			wantPlacements: []string{},
			wantRejections: []string{"t-pinned/gpu_model_not_allowed"},
			wantDetails:    map[string]string{"t-pinned": model3090},
		},
		{
			name: "an exact gpu_models match places the template",
			unit: gpu("gpu-1", model4090, 24*gib),
			catalog: []templates.Template{withRequirements(tpl("t-pinned", 10),
				templates.Requirements{GPUModels: []string{model4090, model3090}})},
			enabled:        []string{"t-pinned"},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{"t-pinned/primary/placed_primary"},
			wantRejections: []string{},
		},
		{
			// Both axes are independent filters; passing one does not
			// excuse the other.
			name: "class may pass while model fails",
			unit: gpu("gpu-1", model4090, 24*gib),
			catalog: []templates.Template{withRequirements(tpl("t-pinned", 10), templates.Requirements{
				GPUClasses: []string{ClassRTX4090},
				GPUModels:  []string{"NVIDIA GeForce RTX 4090 D"},
			})},
			enabled:        []string{"t-pinned"},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{},
			wantRejections: []string{"t-pinned/gpu_model_not_allowed"},
		},
		{
			name: "a card under the VRAM floor is rejected and told how much it has",
			unit: gpu("gpu-1", model1080, 6*gib),
			catalog: []templates.Template{withRequirements(tpl("t-transcode", 10),
				templates.Requirements{GPUVRAMMinBytes: 8 * gib})},
			enabled:        []string{"t-transcode"},
			wantClass:      ClassGTX1080,
			wantPlacements: []string{},
			wantRejections: []string{"t-transcode/insufficient_vram"},
			wantDetails:    map[string]string{"t-transcode": "card reports 6GiB"},
		},
		{
			// The floor is a minimum, so a card that reports exactly it
			// qualifies — catalog floors are set just under the round
			// number a card actually reports.
			name: "a card exactly at the VRAM floor qualifies",
			unit: gpu("gpu-1", model1080, 8*gib),
			catalog: []templates.Template{withRequirements(tpl("t-transcode", 10),
				templates.Requirements{GPUVRAMMinBytes: 8 * gib})},
			enabled:        []string{"t-transcode"},
			wantClass:      ClassGTX1080,
			wantPlacements: []string{"t-transcode/primary/placed_primary"},
			wantRejections: []string{},
		},
		{
			// An agent that did not report VRAM has told the pool
			// nothing, and a fact the pool does not have is not grounds
			// to idle a member's card.
			name: "an unreported VRAM figure does not trip the floor",
			unit: gpu("gpu-1", model4090, 0),
			catalog: []templates.Template{withRequirements(tpl("t-chat", 30),
				templates.Requirements{GPUVRAMMinBytes: 24 * gib})},
			enabled:        []string{"t-chat"},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{"t-chat/primary/placed_primary"},
			wantRejections: []string{},
		},
		{
			name:    "an opt-out with no hardware unit covers every card the member has",
			unit:    gpu("gpu-1", model4090, 24*gib),
			catalog: []templates.Template{tpl("t-images", 28)},
			enabled: []string{"t-images"},
			optOuts: []types.MemberTemplateOptOut{
				{ID: "opt-1", MemberEthAddress: testMember, TemplateID: "t-images"},
			},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{},
			wantRejections: []string{"t-images/member_opted_out"},
		},
		{
			name:    "an opt-out naming one card covers only that card",
			unit:    gpu("gpu-1", model4090, 24*gib),
			catalog: []templates.Template{tpl("t-images", 28)},
			enabled: []string{"t-images"},
			optOuts: []types.MemberTemplateOptOut{
				{ID: "opt-1", MemberEthAddress: testMember, TemplateID: "t-images", HardwareUnitID: "gpu-1"},
			},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{},
			wantRejections: []string{"t-images/member_opted_out"},
		},
		{
			name:    "an opt-out naming another card does not touch this one",
			unit:    gpu("gpu-1", model4090, 24*gib),
			catalog: []templates.Template{tpl("t-images", 28)},
			enabled: []string{"t-images"},
			optOuts: []types.MemberTemplateOptOut{
				{ID: "opt-1", MemberEthAddress: testMember, TemplateID: "t-images", HardwareUnitID: "gpu-2"},
			},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{"t-images/primary/placed_primary"},
			wantRejections: []string{},
		},
		{
			// One member cannot decline work on another member's behalf.
			name:    "another member's opt-out is not honoured here",
			unit:    gpu("gpu-1", model4090, 24*gib),
			catalog: []templates.Template{tpl("t-images", 28)},
			enabled: []string{"t-images"},
			optOuts: []types.MemberTemplateOptOut{
				{ID: "opt-1", MemberEthAddress: otherMember, TemplateID: "t-images"},
			},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{"t-images/primary/placed_primary"},
			wantRejections: []string{},
		},
		{
			// Addresses arrive in EIP-55 checksum case from a wallet and
			// in lower case from the database; a member who opted out
			// must stay opted out either way.
			name:    "an opt-out matches the member address regardless of case",
			unit:    ownedBy(gpu("gpu-1", model4090, 24*gib), "0xabcd000000000000000000000000000000000001"),
			catalog: []templates.Template{tpl("t-images", 28)},
			enabled: []string{"t-images"},
			optOuts: []types.MemberTemplateOptOut{
				{ID: "opt-1", MemberEthAddress: "  0xAbCd000000000000000000000000000000000001 ", TemplateID: "t-images"},
			},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{},
			wantRejections: []string{"t-images/member_opted_out"},
		},
		{
			name: "a secondary-only template never claims a card as primary",
			unit: gpu("gpu-1", model4090, 24*gib),
			catalog: []templates.Template{withStacking(tpl("t-audio", 12),
				templates.Stacking{Primary: false, SecondaryOn: []string{ClassRTX4090}})},
			enabled:        []string{"t-audio"},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{},
			wantRejections: []string{"t-audio/not_stackable_on_this_class"},
			wantDetails: map[string]string{
				"t-audio": "template is secondary-only and no eligible template can claim this GPU as primary",
			},
		},
		{
			name: "a secondary rides alongside a primary when it declares the class",
			unit: gpu("gpu-1", model4090, 24*gib),
			catalog: []templates.Template{
				tpl("t-chat", 30),
				withStacking(tpl("t-audio", 12), templates.Stacking{
					Primary: true, SecondaryOn: []string{ClassRTX4090, ClassRTX5090},
				}),
			},
			enabled:   []string{"t-chat", "t-audio"},
			wantClass: ClassRTX4090,
			wantPlacements: []string{
				"t-chat/primary/placed_primary",
				"t-audio/secondary/placed_secondary",
			},
			wantRejections: []string{},
		},
		{
			name: "a secondary that does not declare this class stays off the card",
			unit: gpu("gpu-1", model4090, 24*gib),
			catalog: []templates.Template{
				tpl("t-chat", 30),
				withStacking(tpl("t-audio", 12), templates.Stacking{
					Primary: true, SecondaryOn: []string{ClassRTX5090},
				}),
			},
			enabled:        []string{"t-chat", "t-audio"},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{"t-chat/primary/placed_primary"},
			wantRejections: []string{"t-audio/not_stackable_on_this_class"},
			wantDetails: map[string]string{
				"t-audio": "template does not declare rtx-4090 in stacking.secondary_on",
			},
		},
		{
			// The template says it may stack here; the class says it may
			// not. Capacity is the operator's call, so the class wins.
			name: "the class stance refuses a stack the template would allow",
			unit: gpu("gpu-1", model3090, 24*gib),
			catalog: []templates.Template{
				tpl("t-embed", 20),
				withStacking(tpl("t-audio", 12), templates.Stacking{
					Primary: true, SecondaryOn: []string{ClassRTX3090},
				}),
			},
			enabled:        []string{"t-embed", "t-audio"},
			wantClass:      ClassRTX3090,
			wantPlacements: []string{"t-embed/primary/placed_primary"},
			wantRejections: []string{"t-audio/stacking_limit_reached"},
			wantDetails:    map[string]string{"t-audio": "rtx-3090 takes 1 template(s)"},
		},
		{
			name: "an operator stance override opens stacking on a 3090",
			unit: gpu("gpu-1", model3090, 24*gib),
			catalog: []templates.Template{
				tpl("t-embed", 20),
				withStacking(tpl("t-audio", 12), templates.Stacking{
					Primary: true, SecondaryOn: []string{ClassRTX3090},
				}),
			},
			enabled:   []string{"t-embed", "t-audio"},
			stances:   map[string]int{ClassRTX3090: 2},
			wantClass: ClassRTX3090,
			wantPlacements: []string{
				"t-embed/primary/placed_primary",
				"t-audio/secondary/placed_secondary",
			},
			wantRejections: []string{},
		},
		{
			name: "an operator stance override closes stacking on a 4090",
			unit: gpu("gpu-1", model4090, 24*gib),
			catalog: []templates.Template{
				tpl("t-chat", 30),
				withStacking(tpl("t-audio", 12), templates.Stacking{
					Primary: true, SecondaryOn: []string{ClassRTX4090},
				}),
			},
			enabled:        []string{"t-chat", "t-audio"},
			stances:        map[string]int{ClassRTX4090: 1},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{"t-chat/primary/placed_primary"},
			wantRejections: []string{"t-audio/stacking_limit_reached"},
		},
		{
			// A suspended card is not a placement question, and the plan
			// says so rather than silently reporting "nothing matched",
			// which would send an operator hunting through requirements.
			name:           "a suspended GPU takes nothing",
			unit:           suspend(gpu("gpu-1", model4090, 24*gib), types.HardwareUnitSuspended),
			catalog:        []templates.Template{tpl("t-chat", 30)},
			enabled:        []string{"t-chat"},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{},
			wantRejections: []string{"/hardware_not_placeable"},
			wantDetails:    map[string]string{"": "suspended"},
		},
		{
			name:           "a retired GPU takes nothing",
			unit:           suspend(gpu("gpu-1", model4090, 24*gib), types.HardwareUnitRetired),
			catalog:        []templates.Template{tpl("t-chat", 30)},
			enabled:        []string{"t-chat"},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{},
			wantRejections: []string{"/hardware_not_placeable"},
			wantDetails:    map[string]string{"": "retired"},
		},
		{
			// Certification is a state a card passes through on its way
			// to earning; it keeps its plan while it does.
			name:           "a GPU mid-certification still gets a plan",
			unit:           suspend(gpu("gpu-1", model4090, 24*gib), types.HardwareUnitTesting),
			catalog:        []templates.Template{tpl("t-chat", 30)},
			enabled:        []string{"t-chat"},
			wantClass:      ClassRTX4090,
			wantPlacements: []string{"t-chat/primary/placed_primary"},
			wantRejections: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			decisions := Plan(Input{
				Hardware:  []types.HardwareUnit{tc.unit},
				Templates: tc.catalog,
				Overrides: buildOverrides(tc.enabled, tc.disabled),
				OptOuts:   tc.optOuts,
				Stances:   tc.stances,
			})
			if len(decisions) != 1 {
				t.Fatalf("Plan() returned %d decisions, want 1", len(decisions))
			}
			got := decisions[0]
			if got.HardwareUnitID != tc.unit.ID || got.MemberEthAddress != tc.unit.MemberEthAddress {
				t.Errorf("decision identifies %s/%s, want %s/%s",
					got.HardwareUnitID, got.MemberEthAddress, tc.unit.ID, tc.unit.MemberEthAddress)
			}
			if got.GPUClass != tc.wantClass {
				t.Errorf("gpu_class = %q, want %q", got.GPUClass, tc.wantClass)
			}
			if gotP := placementStrings(got); !reflect.DeepEqual(gotP, tc.wantPlacements) {
				t.Errorf("placements = %v, want %v", gotP, tc.wantPlacements)
			}
			if gotR := rejectionStrings(got); !reflect.DeepEqual(gotR, tc.wantRejections) {
				t.Errorf("rejections = %v, want %v", gotR, tc.wantRejections)
			}
			for id, wantDetail := range tc.wantDetails {
				found := false
				for _, r := range got.Rejections {
					if r.TemplateID != id {
						continue
					}
					found = true
					if r.Detail != wantDetail {
						t.Errorf("rejection %q detail = %q, want %q", id, r.Detail, wantDetail)
					}
				}
				if !found {
					t.Errorf("no rejection for %q, wanted detail %q", id, wantDetail)
				}
			}
		})
	}
}

// A high-priority secondary-only template is dropped from the card
// entirely, even though a lower-priority primary goes on to claim it.
// A rider-only template must still be placed when it outranks the
// template that ends up claiming the card.
//
// The primary is chosen first, from the templates that can be one, and
// only then are the rest considered as riders. Deciding both in one
// priority-ordered pass would reject a secondary-only template for
// having no primary to ride on before a primary had been chosen, and
// never reconsider it — so a high-priority rider would silently never
// be placed anywhere.
func TestPlanSecondaryOnlyTemplateOutrankingThePrimaryStillRides(t *testing.T) {
	decisions := Plan(Input{
		Hardware: []types.HardwareUnit{gpu("gpu-1", model4090, 24*gib)},
		Templates: []templates.Template{
			withStacking(tpl("t-rider", 40), templates.Stacking{
				Primary: false, SecondaryOn: []string{ClassRTX4090},
			}),
			tpl("t-chat", 30),
		},
		Overrides: buildOverrides([]string{"t-rider", "t-chat"}, nil),
	})
	got := placementStrings(decisions[0])
	want := []string{"t-chat/primary/placed_primary", "t-rider/secondary/placed_secondary"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("placements = %v, want %v", got, want)
	}
}

// With nothing able to claim the card, a rider has nothing to ride on
// and the reason says so.
func TestPlanSecondaryOnlyTemplateWithNoPrimaryIsRejected(t *testing.T) {
	decisions := Plan(Input{
		Hardware: []types.HardwareUnit{gpu("gpu-1", model4090, 24*gib)},
		Templates: []templates.Template{
			withStacking(tpl("t-rider", 40), templates.Stacking{
				Primary: false, SecondaryOn: []string{ClassRTX4090},
			}),
		},
		Overrides: buildOverrides([]string{"t-rider"}, nil),
	})
	if got := placementStrings(decisions[0]); len(got) != 0 {
		t.Fatalf("placements = %v, want none", got)
	}
	if gotR := rejectionStrings(decisions[0]); !reflect.DeepEqual(gotR, []string{"t-rider/not_stackable_on_this_class"}) {
		t.Fatalf("rejections = %v, want t-rider rejected as unstackable", gotR)
	}
}

// A member with two cards may decline a workload on one of them and
// keep earning on the other — that is the whole reason opt-outs carry a
// hardware unit id.
func TestPlanOptOutIsPerCard(t *testing.T) {
	decisions := Plan(Input{
		Hardware: []types.HardwareUnit{
			gpu("gpu-1", model4090, 24*gib),
			gpu("gpu-2", model4090, 24*gib),
		},
		Templates: []templates.Template{tpl("t-images", 28)},
		Overrides: buildOverrides([]string{"t-images"}, nil),
		OptOuts: []types.MemberTemplateOptOut{
			{ID: "opt-1", MemberEthAddress: testMember, TemplateID: "t-images", HardwareUnitID: "gpu-2"},
		},
	})
	if len(decisions) != 2 {
		t.Fatalf("Plan() returned %d decisions, want 2", len(decisions))
	}
	if got := placementStrings(decisions[0]); !reflect.DeepEqual(got, []string{"t-images/primary/placed_primary"}) {
		t.Errorf("gpu-1 placements = %v, want the template placed", got)
	}
	if got := rejectionStrings(decisions[1]); !reflect.DeepEqual(got, []string{"t-images/member_opted_out"}) {
		t.Errorf("gpu-2 rejections = %v, want member_opted_out", got)
	}
}

// The plan becomes a desired-state revision the member's agent polls.
// If unchanged inputs produced a differently ordered plan, every poll
// would look like a change and restart the member's containers.
func TestPlanIsDeterministic(t *testing.T) {
	catalog := []templates.Template{
		withStacking(tpl("t-audio", 12), templates.Stacking{
			Primary: true, SecondaryOn: []string{ClassRTX4090},
		}),
		tpl("t-chat", 30),
		withRequirements(tpl("t-embed", 20),
			templates.Requirements{GPUClasses: []string{ClassRTX3090, ClassRTX4090}}),
	}
	forward := Input{
		Hardware: []types.HardwareUnit{
			gpu("gpu-3", model1080, 8*gib),
			gpu("gpu-1", model4090, 24*gib),
			gpu("gpu-2", model3090, 24*gib),
		},
		Templates: catalog,
		Overrides: buildOverrides([]string{"t-audio", "t-chat", "t-embed"}, nil),
	}
	// Same facts, different arrival order — the repo's list order is
	// not a promise the engine may depend on.
	reversed := Input{
		Hardware: []types.HardwareUnit{
			gpu("gpu-2", model3090, 24*gib),
			gpu("gpu-1", model4090, 24*gib),
			gpu("gpu-3", model1080, 8*gib),
		},
		Templates: []templates.Template{catalog[1], catalog[2], catalog[0]},
		Overrides: buildOverrides([]string{"t-embed", "t-chat", "t-audio"}, nil),
	}
	first, err := json.Marshal(Plan(forward))
	if err != nil {
		t.Fatalf("marshal first plan: %v", err)
	}
	second, err := json.Marshal(Plan(reversed))
	if err != nil {
		t.Fatalf("marshal second plan: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("plan depends on input order:\n first=%s\nsecond=%s", first, second)
	}
	decisions := Plan(forward)
	if len(decisions) != 3 {
		t.Fatalf("Plan() returned %d decisions, want 3", len(decisions))
	}
	for i, want := range []string{"gpu-1", "gpu-2", "gpu-3"} {
		if decisions[i].HardwareUnitID != want {
			t.Errorf("decision[%d] is %s, want %s (GPUs are ordered by id)", i, decisions[i].HardwareUnitID, want)
		}
	}
}

func buildOverrides(enabled, disabled []string) []types.TemplateOverride {
	out := make([]types.TemplateOverride, 0, len(enabled)+len(disabled))
	for _, id := range enabled {
		out = append(out, types.TemplateOverride{TemplateID: id, Enabled: true})
	}
	for _, id := range disabled {
		out = append(out, types.TemplateOverride{TemplateID: id, Enabled: false})
	}
	return out
}

func ownedBy(unit types.HardwareUnit, member string) types.HardwareUnit {
	unit.MemberEthAddress = member
	return unit
}

func suspend(unit types.HardwareUnit, state types.HardwareUnitState) types.HardwareUnit {
	unit.State = state
	return unit
}
