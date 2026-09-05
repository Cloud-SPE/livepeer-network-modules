package placement

import (
	"sort"
	"strconv"
	"strings"

	gpuv "github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/gpu"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// Reason codes explain why a GPU is running what it is running — or
// nothing. An operator asking "why is this card idle" and a member
// asking "why is my 3090 not earning" get the same answer, and neither
// has to read the policy to find it.
const (
	ReasonPlacedPrimary    = "placed_primary"
	ReasonPlacedSecondary  = "placed_secondary"
	ReasonUnknownGPUClass  = "gpu_class_unknown"
	ReasonClassNotAllowed  = "gpu_class_not_allowed"
	ReasonModelNotAllowed  = "gpu_model_not_allowed"
	ReasonInsufficientVRAM = "insufficient_vram"
	ReasonMemberOptedOut   = "member_opted_out"
	ReasonNotStackable     = "not_stackable_on_this_class"
	ReasonStackingFull     = "stacking_limit_reached"
	ReasonHardwareNotReady = "hardware_not_placeable"
	// ReasonNoImageForVendor: the template ships a runner, but not one
	// built for this card's vendor. Rejected here rather than rendered,
	// because the alternative is a compose pull that fails on a
	// member's host (plan 0045 §4).
	ReasonNoImageForVendor = "no_image_for_vendor"
	// ReasonHostNotPublic: a paid-session template on a host that
	// declares no public_url. Every session data plane is external
	// (offering-axes §3), so a host nobody can reach cannot serve one.
	ReasonHostNotPublic = "host_not_public"
	// ReasonKindNotAllowed: the template admits the other kind of
	// compute unit — a socket offered to a GPU template, or a card to a
	// CPU-only one. The two never compete.
	ReasonKindNotAllowed = "kind_not_allowed"
)

// Placement is one template placed on one GPU.
type Placement struct {
	TemplateID string                       `json:"template_id"`
	Role       types.TemplateAssignmentRole `json:"role"`
	Reason     string                       `json:"reason"`
}

// Rejection is a template that could have run here and did not.
type Rejection struct {
	TemplateID string `json:"template_id"`
	Reason     string `json:"reason"`
	// Detail carries the specific value that failed, so a member is
	// told "needs 24GB, card has 11GB" rather than just "insufficient".
	Detail string `json:"detail,omitempty"`
}

// Decision is the desired state for one GPU.
type Decision struct {
	HardwareUnitID string `json:"hardware_unit_id"`
	// HostEnrollmentID travels with the decision so an assignment made
	// from it knows which host to reach. Without it, certification runs
	// created from a placed assignment carry an empty enrollment.
	HostEnrollmentID string      `json:"host_enrollment_id,omitempty"`
	MemberEthAddress string      `json:"member_eth_address"`
	GPUClass         string      `json:"gpu_class,omitempty"`
	Placements       []Placement `json:"placements"`
	Rejections       []Rejection `json:"rejections,omitempty"`
}

// Input is everything the engine needs. It takes plain slices rather
// than a repo so the policy can be reasoned about — and tested —
// without a database.
type Input struct {
	Hardware  []types.HardwareUnit
	Templates []templates.Template
	Overrides []types.TemplateOverride
	OptOuts   []types.MemberTemplateOptOut
	// Stances overrides DefaultStances per GPU class.
	Stances map[string]int
}

// NotEnabled lists the catalog templates this pool has not enabled.
//
// It is reported once for the whole plan rather than as a rejection on
// every GPU, because it is a pool-wide fact and not a property of any
// card. Without it the most likely answer to "why is nothing running
// this workload" — nobody switched it on — is the one answer the plan
// does not give.
func NotEnabled(all []templates.Template, overrides []types.TemplateOverride) []string {
	byID := make(map[string]types.TemplateOverride, len(overrides))
	for _, override := range overrides {
		byID[override.TemplateID] = override
	}
	out := make([]string, 0)
	for _, tmpl := range all {
		if override, ok := byID[tmpl.ID]; !ok || !override.Enabled {
			out = append(out, tmpl.ID)
		}
	}
	sort.Strings(out)
	return out
}

// Plan computes the desired placement for every GPU.
//
// Order is deterministic — GPUs by id, templates by priority then id —
// because this output becomes a desired-state revision the agent
// pulls. Two runs over unchanged inputs must produce an identical plan,
// or every poll would look like a change and restart the member's
// containers.
func Plan(in Input) []Decision {
	enabled := enabledTemplates(in.Templates, in.Overrides)
	out := make([]Decision, 0, len(in.Hardware))
	units := append([]types.HardwareUnit(nil), in.Hardware...)
	sort.Slice(units, func(i, j int) bool { return units[i].ID < units[j].ID })
	for _, unit := range units {
		out = append(out, planUnit(unit, enabled, in.OptOuts, in.Stances))
	}
	return out
}

// enabledTemplates keeps only what this pool sells, ordered by the
// priority that decides which one claims a GPU as primary. Ties break
// on id so the plan does not depend on catalog file order.
func enabledTemplates(all []templates.Template, overrides []types.TemplateOverride) []templates.Template {
	byID := make(map[string]types.TemplateOverride, len(overrides))
	for _, override := range overrides {
		byID[override.TemplateID] = override
	}
	out := make([]templates.Template, 0, len(all))
	for _, tmpl := range all {
		if override, ok := byID[tmpl.ID]; ok && override.Enabled {
			out = append(out, tmpl)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority > out[j].Priority
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func planUnit(unit types.HardwareUnit, enabled []templates.Template,
	optOuts []types.MemberTemplateOptOut, stances map[string]int) Decision {

	class := ClassOfUnit(unit)
	decision := Decision{
		HardwareUnitID:   unit.ID,
		HostEnrollmentID: unit.EnrollmentID,
		MemberEthAddress: unit.MemberEthAddress,
		GPUClass:         class,
		Placements:       []Placement{},
	}
	// A retired or suspended card is not a placement question. Leaving
	// it out of the plan entirely would read as "no templates matched",
	// which is a different and misleading answer.
	if !placeable(unit.State) {
		decision.Rejections = append(decision.Rejections, Rejection{
			Reason: ReasonHardwareNotReady,
			Detail: string(unit.State),
		})
		return decision
	}

	// Which templates could run here at all, in priority order.
	eligible := make([]templates.Template, 0, len(enabled))
	for _, tmpl := range enabled {
		if optedOut(optOuts, unit, tmpl.ID) {
			decision.Rejections = append(decision.Rejections, Rejection{
				TemplateID: tmpl.ID, Reason: ReasonMemberOptedOut,
			})
			continue
		}
		if reason, detail := requirementsFail(tmpl, unit, class); reason != "" {
			decision.Rejections = append(decision.Rejections, Rejection{
				TemplateID: tmpl.ID, Reason: reason, Detail: detail,
			})
			continue
		}
		eligible = append(eligible, tmpl)
	}

	// The primary is chosen first, from the templates that can BE a
	// primary. Doing this in one pass would let a secondary-only
	// template sorted above them be rejected for having no primary to
	// ride on, and never reconsidered once one was chosen — so a
	// rider-only workload with a high priority would silently never be
	// placed anywhere.
	primary := -1
	for i, tmpl := range eligible {
		if tmpl.Stacking.Primary {
			primary = i
			break
		}
	}
	if primary >= 0 {
		decision.Placements = append(decision.Placements, Placement{
			TemplateID: eligible[primary].ID,
			Role:       types.TemplateAssignmentPrimary,
			Reason:     ReasonPlacedPrimary,
		})
	}

	limit := MaxTemplatesFor(class, stances)
	for i, tmpl := range eligible {
		if i == primary {
			continue
		}
		if primary < 0 {
			// Nothing claimed the card, so there is nothing to ride on.
			decision.Rejections = append(decision.Rejections, Rejection{
				TemplateID: tmpl.ID, Reason: ReasonNotStackable,
				Detail: "template is secondary-only and no eligible template can claim this GPU as primary",
			})
			continue
		}
		if len(decision.Placements) >= limit {
			decision.Rejections = append(decision.Rejections, Rejection{
				TemplateID: tmpl.ID, Reason: ReasonStackingFull,
				Detail: class + " takes " + strconv.Itoa(limit) + " template(s)",
			})
			continue
		}
		if !stackableOn(tmpl, class) {
			decision.Rejections = append(decision.Rejections, Rejection{
				TemplateID: tmpl.ID, Reason: ReasonNotStackable,
				Detail: "template does not declare " + displayClass(class) + " in stacking.secondary_on",
			})
			continue
		}
		decision.Placements = append(decision.Placements, Placement{
			TemplateID: tmpl.ID,
			Role:       types.TemplateAssignmentSecondary,
			Reason:     ReasonPlacedSecondary,
		})
	}
	return decision
}

// requirementsFail applies the hardware gate. Each axis is an
// independent filter and an empty one is no constraint — a template
// naming no models is not a template that matches no cards. Getting
// this backwards would place every workload on every GPU, so the two
// axes are checked separately and both must pass.
func requirementsFail(tmpl templates.Template, unit types.HardwareUnit, class string) (string, string) {
	req := tmpl.Requirements
	// A socket and a card are different kinds of unit and a template
	// admits one kind by listing its classes (plan 0047). A socket is
	// never the default winner of an unconstrained template — that was
	// the §5 failure mode for cards, and it would be worse here.
	if unit.IsCPU() {
		if len(req.CPUClasses) == 0 {
			return ReasonKindNotAllowed, "template admits no cpu unit"
		}
		if class == ClassUnknown {
			return ReasonUnknownGPUClass, unit.GPUModel + " (" + strconv.Itoa(unit.Cores) + " cores)"
		}
		if !containsFold(req.CPUClasses, class) {
			return ReasonClassNotAllowed, class
		}
		if tmpl.RunnerCompose.HasImage() && tmpl.RunnerCompose.ImageFor(gpuv.VendorCPU) == "" {
			return ReasonNoImageForVendor, "no cpu image"
		}
		return "", ""
	}
	if len(req.CPUClasses) > 0 && len(req.GPUClasses) == 0 && len(req.GPUModels) == 0 {
		return ReasonKindNotAllowed, "template admits cpu units only"
	}
	if len(req.GPUClasses) > 0 {
		if class == ClassUnknown {
			return ReasonUnknownGPUClass, unit.GPUModel
		}
		if !containsFold(req.GPUClasses, class) {
			return ReasonClassNotAllowed, class
		}
	}
	if len(req.GPUModels) > 0 && !containsFold(req.GPUModels, unit.GPUModel) {
		return ReasonModelNotAllowed, unit.GPUModel
	}
	// A session's caller connects to the runner directly, so the host
	// has to be reachable from outside. The fact is the host's
	// public_url (runner-attach §3.1), relayed onto its units; a host
	// without one sits on the exception queue for every session
	// template rather than being placed on work nobody can reach.
	if tmpl.Protocol == "paid-session/v1" && strings.TrimSpace(unit.PublicURL) == "" {
		return ReasonHostNotPublic, "host declares no public_url"
	}
	if req.GPUVRAMMinBytes > 0 && unit.VRAMBytes > 0 && unit.VRAMBytes < req.GPUVRAMMinBytes {
		return ReasonInsufficientVRAM, "card reports " + strconv.FormatUint(unit.VRAMBytes/(1<<30), 10) + "GiB"
	}
	// The hardware gate's last axis: the template has to ship a build
	// for this card's vendor. A template with no image at all is not
	// gated — it places and renders no service, as before — so a
	// catalog entry whose images are still unresolved keeps its slot
	// rather than silently handing the card to something else.
	if tmpl.RunnerCompose.HasImage() {
		vendor := gpuv.VendorOfModel(unit.GPUModel)
		if vendor == "" && tmpl.RunnerCompose.ImageFor(gpuv.VendorAny) == "" {
			return ReasonNoImageForVendor, "card names no vendor the pool can render for: " + unit.GPUModel
		}
		if tmpl.RunnerCompose.ImageForClass(vendor, class) == "" {
			return ReasonNoImageForVendor, "no " + vendor + " image for " + class
		}
	}
	return "", ""
}

func stackableOn(tmpl templates.Template, class string) bool {
	return containsFold(tmpl.Stacking.SecondaryOn, class)
}

func optedOut(optOuts []types.MemberTemplateOptOut, unit types.HardwareUnit, templateID string) bool {
	member := strings.ToLower(strings.TrimSpace(unit.MemberEthAddress))
	for _, optOut := range optOuts {
		if optOut.TemplateID != templateID {
			continue
		}
		if strings.ToLower(strings.TrimSpace(optOut.MemberEthAddress)) != member {
			continue
		}
		if optOut.Covers(unit.ID) {
			return true
		}
	}
	return false
}

// placeable reports whether a GPU is in a state that can take work. A
// card mid-certification keeps what it has; one suspended or retired
// takes nothing new.
func placeable(state types.HardwareUnitState) bool {
	switch state {
	case types.HardwareUnitSuspended, types.HardwareUnitRetired:
		return false
	default:
		return true
	}
}

func containsFold(list []string, want string) bool {
	for _, item := range list {
		if strings.EqualFold(strings.TrimSpace(item), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}

func displayClass(class string) string {
	if class == ClassUnknown {
		return "this GPU class"
	}
	return class
}
