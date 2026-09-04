// Package desiredstate renders what one enrolled host should be running.
//
// The controller decides placement; this turns those placements into
// something an agent can act on without knowing any pool policy — a
// compose fragment per service, the GPUs it may use, and the model
// weights it needs on disk first. The agent's job is then mechanical:
// pull, download, `compose up`, report.
package desiredstate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/gpu"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/placement"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// Document is what GET /member/v1/enrollments/{id}/desired-state returns.
type Document struct {
	EnrollmentID string    `json:"enrollment_id"`
	Revision     string    `json:"revision"`
	Services     []Service `json:"services"`
}

// Service is one runner the agent should have running.
type Service struct {
	Name string `json:"name"`
	// ComposeFragment is a docker-compose service body, already
	// indented to sit under `services:`. The agent merges it rather
	// than composing one itself, so what runs is what the pool
	// authored — an agent that built its own would drift from the
	// template the moment either changed.
	ComposeFragment string `json:"compose_fragment"`
	// DeviceIDs are the GPU UUIDs this service may use. A host with
	// several cards runs several services, and each must be pinned to
	// its own or they would contend for the same device.
	DeviceIDs []string `json:"device_ids"`
	Models    []Model  `json:"models,omitempty"`
	// Draining says the pool is withdrawing this service. The agent
	// marks it draining in its attach document so the broker stops
	// dispatching, and stops it once quiet — it is still listed here
	// precisely because it must not simply vanish.
	Draining bool `json:"draining,omitempty"`
	// TemplateID and AssignmentID are for the agent's own reporting and
	// for an operator correlating a container back to a decision.
	TemplateID   string `json:"template_id"`
	AssignmentID string `json:"assignment_id"`

	// Capability and Identity are what the agent must DECLARE to the
	// broker for this runner. Without them the agent cannot build a
	// valid attach document at all: an offer selects its runners on
	// declared identity, so a runner that declares none matches
	// nothing — and the openai profile refuses to build without a
	// model. The controller knows both from the template (its
	// capability, and the identity its `match` selects on), so it says
	// so rather than leaving the agent to guess.
	Capability string            `json:"capability"`
	Protocol   string            `json:"protocol,omitempty"`
	Identity   map[string]string `json:"identity,omitempty"`
}

// Model is a weight file that must be on disk before the service starts.
type Model struct {
	Name      string `json:"name"`
	SizeBytes uint64 `json:"size_bytes,omitempty"`
	Source    string `json:"source,omitempty"`
}

// Input is the state one host's desired document is built from.
type Input struct {
	EnrollmentID string
	Assignments  []types.TemplateAssignment
	Hardware     []types.HardwareUnit
	Catalog      *templates.Catalog
}

// Build renders the document for one host.
//
// The revision is a hash of the rendered services, so it changes when
// and only when what the agent should run changes. An agent that polls
// and sees its own revision does nothing — which is the common case,
// and the reason a poll is cheap enough to be frequent.
func Build(in Input) (Document, error) {
	unitsByID := make(map[string]types.HardwareUnit, len(in.Hardware))
	for _, unit := range in.Hardware {
		unitsByID[unit.ID] = unit
	}
	services := make([]Service, 0, len(in.Assignments))
	for _, assignment := range in.Assignments {
		if assignment.State == types.TemplateAssignmentRetired {
			// Retired is gone, not withdrawn: there is nothing left to
			// drain and listing it would keep resurrecting it.
			continue
		}
		unit, ok := unitsByID[assignment.HardwareUnitID]
		if !ok {
			continue
		}
		// A card the pool has taken back — retired after a transfer, or
		// suspended — must stop serving even though the placement on it
		// has not been withdrawn yet.
		//
		// Placement would drain it on its next plan, but nothing applies
		// that plan on a timer, so between the two this host would keep
		// being told to run a container for hardware it no longer holds.
		// Rendering it as draining rather than dropping it is the same
		// rule as everywhere else: the broker stops dispatching first
		// and work already in flight finishes.
		withdrawn := assignment.State == types.TemplateAssignmentDraining || !servingUnit(unit)
		tmpl, known := in.Catalog.Get(assignment.TemplateID)
		if !known {
			// A placement naming a template this build does not ship
			// cannot be rendered. Skipping it silently would leave the
			// agent running nothing with no explanation, so it is an
			// error the operator sees.
			return Document{}, fmt.Errorf("assignment %s names template %q, which is not in the catalog",
				assignment.ID, assignment.TemplateID)
		}
		services = append(services, Service{
			Name:            ServiceName(assignment.ID),
			Capability:      tmpl.Capability,
			Protocol:        tmpl.Protocol,
			Identity:        identityFor(tmpl),
			ComposeFragment: renderCompose(ServiceName(assignment.ID), tmpl, unit),
			DeviceIDs:       []string{unit.GPUUUID},
			Models:          modelsOf(tmpl),
			Draining:        withdrawn,
			TemplateID:      tmpl.ID,
			AssignmentID:    assignment.ID,
		})
	}
	// Stable order, so an unchanged pool yields an unchanged revision.
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	return Document{
		EnrollmentID: in.EnrollmentID,
		Revision:     Revision(services),
		Services:     services,
	}, nil
}

// Revision hashes the rendered services. It deliberately covers
// everything the agent acts on — including whether a service is
// draining — because a drain the agent does not notice is a container
// that keeps taking work the broker has stopped sending it.
func Revision(services []Service) string {
	raw, err := json.Marshal(services)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "rev-" + hex.EncodeToString(sum[:12])
}

// ServiceName is the compose service name for an assignment. Compose
// names must be a restricted charset, and an assignment id carries a
// separator that is not in it.
func ServiceName(assignmentID string) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '-'
		}
	}, assignmentID)
	return "runner-" + strings.Trim(name, "-")
}

func modelsOf(tmpl templates.Template) []Model {
	if len(tmpl.RunnerCompose.Models) == 0 {
		return nil
	}
	out := make([]Model, 0, len(tmpl.RunnerCompose.Models))
	for _, model := range tmpl.RunnerCompose.Models {
		out = append(out, Model{Name: model.Name, SizeBytes: model.SizeBytes, Source: model.Source})
	}
	return out
}

// renderCompose writes the service body.
//
// The GPU is pinned by UUID rather than `gpus: all`: a host with two
// cards running two workloads must not have both services claim both
// devices, and a UUID is the only identifier stable across reboots.
func renderCompose(name string, tmpl templates.Template, unit types.HardwareUnit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  %s:\n", name)
	// The image is the vendor's build. Placement already refused a card
	// the template has no image for (ReasonNoImageForVendor), so an
	// empty lookup here is a template with no runner_compose at all,
	// which renders no image line — as before.
	vendor := gpu.VendorOfModel(unit.GPUModel)
	if unit.IsCPU() {
		// A socket has no device to pin and no vendor build to choose
		// between: the cpu image, and the container sees the host's
		// cores the way any container does.
		vendor = gpu.VendorCPU
	}
	if image := tmpl.RunnerCompose.ImageForClass(vendor, placement.ClassOfUnit(unit)); image != "" {
		fmt.Fprintf(&b, "    image: %s\n", image)
	}
	b.WriteString("    restart: unless-stopped\n")
	if cmd := tmpl.RunnerCompose.Command; len(cmd) > 0 {
		b.WriteString("    command:\n")
		for _, item := range cmd {
			fmt.Fprintf(&b, "      - %s\n", item)
		}
	}
	// One environment block, whoever supplied the values. The pool's
	// own settings and the member's passthroughs are the same compose
	// key, and emitting two would be duplicate-key YAML the agent could
	// not load.
	//
	// Member values are rendered as ${NAME}: docker substitutes them
	// from the member's own .env at `compose up`, so the pool renders
	// the reference and never the secret.
	env := make(map[string]string, len(tmpl.RunnerCompose.Env))
	for name, value := range tmpl.RunnerCompose.Env {
		env[name] = value
	}
	// A session runner builds its descriptor url from this and never
	// guesses a hostname (plan 0046 §2): the host's public origin plus
	// the path the agent's edge routes to this service. Only rendered
	// for a public host — placement refuses a session template on any
	// other, so an absent value here is a bug upstream, not a fallback.
	if tmpl.Protocol == "paid-session/v1" && strings.TrimSpace(unit.PublicURL) != "" {
		env["LIVEPEER_PUBLIC_URL"] = strings.TrimRight(strings.TrimSpace(unit.PublicURL), "/") + "/r/" + name
	}
	if len(env) > 0 {
		names := make([]string, 0, len(env))
		for name := range env {
			names = append(names, name)
		}
		sort.Strings(names)
		b.WriteString("    environment:\n")
		for _, name := range names {
			fmt.Fprintf(&b, "      %s: %s\n", name, env[name])
		}
	}
	// How the card reaches the container is the vendor's to say, and
	// the two known vendors do it differently. NVIDIA pins one card by
	// UUID through its container runtime, which is what makes a
	// two-card host two services that cannot contend. Intel exposes the
	// DRI render nodes, and compose has no per-card selector for them —
	// so on a multi-card Intel host every service sees every card, and
	// the one-service-per-GPU invariant the UUID pin enforces is not
	// enforced there. Stated here rather than papered over: it is a
	// real limit of the runtime, not of this renderer.
	switch vendor {
	case gpu.VendorNVIDIA:
		if uuid := strings.TrimSpace(unit.GPUUUID); uuid != "" {
			b.WriteString("    deploy:\n      resources:\n        reservations:\n          devices:\n")
			b.WriteString("            - driver: nvidia\n              capabilities: [gpu]\n")
			fmt.Fprintf(&b, "              device_ids: [%q]\n", uuid)
		}
	case gpu.VendorIntel:
		b.WriteString("    devices:\n      - /dev/dri:/dev/dri\n")
	case gpu.VendorAMD:
		// ROCm's standard exposure. No AMD image ships yet; rendering the
		// right devices when one does costs nothing now.
		b.WriteString("    devices:\n      - /dev/dri:/dev/dri\n      - /dev/kfd:/dev/kfd\n")
	}
	return b.String()
}

// identityFor is what this runner must declare so the offer's match
// selects it. The template states the selector as `identity.<key>`;
// the runner declares the bare key, so the prefix is stripped here
// rather than in the agent — the controller owns the mapping between
// what an offer selects on and what a runner says.
func identityFor(tmpl templates.Template) map[string]string {
	if len(tmpl.Match) == 0 {
		return nil
	}
	out := make(map[string]string, len(tmpl.Match))
	for key, value := range tmpl.Match {
		out[strings.TrimPrefix(key, "identity.")] = value
	}
	return out
}

// servingUnit reports whether a GPU may still carry work. It mirrors
// the placement engine's own test — a unit the engine would refuse to
// place on must not keep running what was placed on it earlier.
func servingUnit(unit types.HardwareUnit) bool {
	switch unit.State {
	case types.HardwareUnitSuspended, types.HardwareUnitRetired:
		return false
	default:
		return true
	}
}
