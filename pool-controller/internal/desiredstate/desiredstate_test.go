package desiredstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// templateYAML is the smallest catalog entry that also carries what an
// agent acts on: an image, environment and a weight file.
func templateYAML(id, offeringID, image string) string {
	return "id: " + id + "\n" +
		"capability: openai:chat-completions\n" +
		"offering_id: " + offeringID + "\n" +
		"protocol: paid-job/v1\n" +
		"price_default:\n  amount_wei: \"1\"\n  per_units: 1\n" +
		"stacking:\n  primary: true\n" +
		"runner_compose:\n" +
		"  image: " + image + "\n" +
		"  env:\n    MODEL: llama\n    PORT: \"8080\"\n" +
		"  models:\n    - name: llama-3-8b\n      size_bytes: 42\n      source: hf://meta/llama-3-8b\n"
}

func catalogOf(t *testing.T, files map[string]string) *templates.Catalog {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	catalog, err := templates.Load(dir)
	if err != nil {
		t.Fatalf("templates.Load() error = %v", err)
	}
	return catalog
}

func twoTemplateCatalog(t *testing.T) *templates.Catalog {
	t.Helper()
	return catalogOf(t, map[string]string{
		"chat-a.yaml": templateYAML("chat-a", "a", "ghcr.io/example/a:1"),
		"chat-b.yaml": templateYAML("chat-b", "b", "ghcr.io/example/b:1"),
	})
}

func unit(id, uuid string) types.HardwareUnit {
	return types.HardwareUnit{ID: id, EnrollmentID: "host-1", GPUUUID: uuid, GPUModel: "NVIDIA GeForce RTX 4090"}
}

func assignment(unitID, templateID string, state types.TemplateAssignmentState) types.TemplateAssignment {
	return types.TemplateAssignment{
		ID:               unitID + "|" + templateID,
		HardwareUnitID:   unitID,
		HostEnrollmentID: "host-1",
		TemplateID:       templateID,
		Role:             types.TemplateAssignmentPrimary,
		State:            state,
	}
}

// Each placement becomes one service, and each service is pinned to the
// card the pool placed it on. A host with two GPUs running two
// workloads must not have both services claim both devices, so the
// fragment names the unit's own UUID and never `gpus: all`.
func TestBuildRendersOneServicePerAssignmentPinnedToItsOwnGPU(t *testing.T) {
	doc, err := Build(Input{
		EnrollmentID: "host-1",
		// Deliberately not in name order: the document's order is the
		// builder's, not the caller's.
		Assignments: []types.TemplateAssignment{
			assignment("unit-b", "chat-b", types.TemplateAssignmentActive),
			assignment("unit-a", "chat-a", types.TemplateAssignmentPending),
		},
		Hardware: []types.HardwareUnit{unit("unit-a", "GPU-aaa"), unit("unit-b", "GPU-bbb")},
		Catalog:  twoTemplateCatalog(t),
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if doc.EnrollmentID != "host-1" || doc.Revision == "" {
		t.Fatalf("document = %+v, want the enrollment id and a revision", doc)
	}
	if len(doc.Services) != 2 {
		t.Fatalf("services = %d, want one per assignment", len(doc.Services))
	}
	if doc.Services[0].Name != "runner-unit-a-chat-a" || doc.Services[1].Name != "runner-unit-b-chat-b" {
		t.Fatalf("service order = %q, %q; want sorted by name", doc.Services[0].Name, doc.Services[1].Name)
	}
	for i, want := range []struct{ uuid, image, template, assignmentID string }{
		{"GPU-aaa", "ghcr.io/example/a:1", "chat-a", "unit-a|chat-a"},
		{"GPU-bbb", "ghcr.io/example/b:1", "chat-b", "unit-b|chat-b"},
	} {
		service := doc.Services[i]
		if len(service.DeviceIDs) != 1 || service.DeviceIDs[0] != want.uuid {
			t.Fatalf("service %s device_ids = %v, want exactly [%s]", service.Name, service.DeviceIDs, want.uuid)
		}
		if service.TemplateID != want.template || service.AssignmentID != want.assignmentID {
			t.Fatalf("service %s = template %q assignment %q, want %q / %q",
				service.Name, service.TemplateID, service.AssignmentID, want.template, want.assignmentID)
		}
		if service.Draining {
			t.Fatalf("service %s is draining; neither assignment is", service.Name)
		}
		fragment := service.ComposeFragment
		if !strings.Contains(fragment, "device_ids: [\""+want.uuid+"\"]") {
			t.Fatalf("service %s fragment does not pin %s:\n%s", service.Name, want.uuid, fragment)
		}
		if strings.Contains(fragment, "gpus: all") || strings.Contains(fragment, "count: all") {
			t.Fatalf("service %s claims every GPU; the other card's workload would contend for it:\n%s",
				service.Name, fragment)
		}
		// The other unit's card must not appear at all — a fragment
		// that names both would let one service take the whole host.
		for _, other := range []string{"GPU-aaa", "GPU-bbb"} {
			if other != want.uuid && strings.Contains(fragment, other) {
				t.Fatalf("service %s names another unit's GPU %s:\n%s", service.Name, other, fragment)
			}
		}
		if !strings.Contains(fragment, "image: "+want.image) {
			t.Fatalf("service %s fragment does not carry the template image:\n%s", service.Name, fragment)
		}
		if !strings.HasPrefix(fragment, "  "+service.Name+":\n") {
			t.Fatalf("service %s fragment is not indented under services::\n%s", service.Name, fragment)
		}
		if len(service.Models) != 1 || service.Models[0].Name != "llama-3-8b" ||
			service.Models[0].SizeBytes != 42 || service.Models[0].Source != "hf://meta/llama-3-8b" {
			t.Fatalf("service %s models = %+v, want the template's weight file", service.Name, service.Models)
		}
	}
}

// Retired is gone; draining is on its way out but still serving. The
// draining service has to stay in the document, because an agent that
// stopped seeing it would remove the container mid-request.
func TestBuildOmitsRetiredButKeepsDraining(t *testing.T) {
	doc, err := Build(Input{
		EnrollmentID: "host-1",
		Assignments: []types.TemplateAssignment{
			assignment("unit-a", "chat-a", types.TemplateAssignmentDraining),
			assignment("unit-b", "chat-b", types.TemplateAssignmentRetired),
		},
		Hardware: []types.HardwareUnit{unit("unit-a", "GPU-aaa"), unit("unit-b", "GPU-bbb")},
		Catalog:  twoTemplateCatalog(t),
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(doc.Services) != 1 {
		t.Fatalf("services = %+v, want only the draining one", doc.Services)
	}
	service := doc.Services[0]
	if service.Name != "runner-unit-a-chat-a" {
		t.Fatalf("service = %s, want the draining assignment", service.Name)
	}
	if !service.Draining {
		t.Fatal("draining assignment is listed without draining=true; the agent would keep it in dispatch")
	}
	if service.ComposeFragment == "" {
		t.Fatal("draining service has no compose fragment; the agent could not keep it running while it drains")
	}
}

// A placement naming a template this build does not ship cannot be
// rendered. Skipping it would leave the host running nothing with no
// explanation of why.
func TestBuildRejectsAnAssignmentWhoseTemplateIsNotInTheCatalog(t *testing.T) {
	_, err := Build(Input{
		EnrollmentID: "host-1",
		Assignments: []types.TemplateAssignment{
			assignment("unit-a", "chat-a", types.TemplateAssignmentActive),
			assignment("unit-a", "chat-ghost", types.TemplateAssignmentActive),
		},
		Hardware: []types.HardwareUnit{unit("unit-a", "GPU-aaa")},
		Catalog:  twoTemplateCatalog(t),
	})
	if err == nil {
		t.Fatal("Build() accepted an assignment for a template the catalog does not ship")
	}
	if !strings.Contains(err.Error(), "chat-ghost") || !strings.Contains(err.Error(), "unit-a|chat-ghost") {
		t.Fatalf("Build() error = %v, want it to name the assignment and the missing template", err)
	}
}

// The revision is the agent's whole polling economy: it must change on
// every difference the agent would act on, and on nothing else.
func TestRevisionTracksWhatTheAgentActsOn(t *testing.T) {
	catalog := twoTemplateCatalog(t)
	hardware := []types.HardwareUnit{unit("unit-a", "GPU-aaa"), unit("unit-b", "GPU-bbb")}
	build := func(t *testing.T, assignments []types.TemplateAssignment, cat *templates.Catalog) Document {
		t.Helper()
		doc, err := Build(Input{EnrollmentID: "host-1", Assignments: assignments, Hardware: hardware, Catalog: cat})
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		return doc
	}

	base := []types.TemplateAssignment{
		assignment("unit-a", "chat-a", types.TemplateAssignmentActive),
		assignment("unit-b", "chat-b", types.TemplateAssignmentActive),
	}
	baseRev := build(t, base, catalog).Revision

	// Same inputs in a different order are the same desired state; a
	// revision that moved here would make every poll fetch a body.
	reordered := []types.TemplateAssignment{base[1], base[0]}
	if got := build(t, reordered, catalog).Revision; got != baseRev {
		t.Fatalf("revision changed on input order alone: %s vs %s", baseRev, got)
	}

	// One service removed.
	if got := build(t, base[:1], catalog).Revision; got == baseRev {
		t.Fatal("revision unchanged after a service was removed")
	}

	// One service added.
	added := append(append([]types.TemplateAssignment(nil), base...),
		assignment("unit-a", "chat-b", types.TemplateAssignmentActive))
	if got := build(t, added, catalog).Revision; got == baseRev {
		t.Fatal("revision unchanged after a service was added")
	}

	// One service changed: same placements, new image.
	changed := catalogOf(t, map[string]string{
		"chat-a.yaml": templateYAML("chat-a", "a", "ghcr.io/example/a:2"),
		"chat-b.yaml": templateYAML("chat-b", "b", "ghcr.io/example/b:1"),
	})
	if got := build(t, base, changed).Revision; got == baseRev {
		t.Fatal("revision unchanged after a service's image changed")
	}

	// One service flipped to draining. A drain the agent does not
	// notice is a container that keeps taking work the broker has
	// stopped sending it.
	draining := []types.TemplateAssignment{
		assignment("unit-a", "chat-a", types.TemplateAssignmentDraining),
		base[1],
	}
	if got := build(t, draining, catalog).Revision; got == baseRev {
		t.Fatal("revision unchanged when a service flipped to draining")
	}
}

// Compose names have a restricted charset and an assignment id carries
// a separator that is not in it.
func TestServiceNameIsComposeSafe(t *testing.T) {
	got := ServiceName("unit-A|chat-a.2")
	if got != "runner-unit-a-chat-a-2" {
		t.Fatalf("ServiceName() = %q, want a lowercased compose-safe name", got)
	}
	for _, r := range got {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
		default:
			t.Fatalf("ServiceName() = %q contains %q, which compose will not accept", got, string(r))
		}
	}
	// Distinct assignments must stay distinct services, or two
	// placements would collapse onto one container.
	if ServiceName("unit-a|chat-a") == ServiceName("unit-a|chat-b") {
		t.Fatal("two assignments map to one service name")
	}
}

// The agent rewrites its compose file from every document it fetches,
// and `compose up` acts on the diff. Environment keys therefore have to
// come out sorted rather than in map order, or an unchanged placement
// would look like a change on every poll and restart the container.
func TestRenderedComposeEnvironmentIsSorted(t *testing.T) {
	cat := catalogOf(t, map[string]string{"t.yaml": "" +
		"id: t\n" +
		"capability: openai:chat-completions\n" +
		"offering_id: o\n" +
		"protocol: paid-job/v1\n" +
		"price_default: { amount_wei: \"1\", per_units: 1 }\n" +
		"stacking: { primary: true }\n" +
		"runner_compose:\n" +
		"  image: img\n" +
		"  env: { QUANT: fp8, MODEL: small, ALPHA: \"1\" }\n",
	})
	doc, err := Build(Input{
		EnrollmentID: "host-1",
		Assignments: []types.TemplateAssignment{{
			ID: "unit-a|t", HardwareUnitID: "unit-a", TemplateID: "t",
			MemberEthAddress: "0xa", State: types.TemplateAssignmentActive,
		}},
		Hardware: []types.HardwareUnit{{ID: "unit-a", GPUUUID: "GPU-a", MemberEthAddress: "0xa"}},
		Catalog:  cat,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := "    environment:\n      ALPHA: 1\n      MODEL: small\n      QUANT: fp8\n"
	if !strings.Contains(doc.Services[0].ComposeFragment, want) {
		t.Fatalf("environment not sorted:\n%s", doc.Services[0].ComposeFragment)
	}
}

// A GPU the pool has taken back must stop serving.
//
// `transfer` on a contested card retires the incumbent's unit, but the
// placement on it is not withdrawn in the same gesture — the placement
// engine would drain it on its next plan, and nothing applies that plan
// on a timer. Without this the incumbent's agent keeps running a
// container for hardware that now belongs to someone else, for as long
// as it takes an operator to notice.
func TestBuildDrainsAPlacementOnAWithdrawnCard(t *testing.T) {
	cat := catalogOf(t, map[string]string{"t.yaml": "" +
		"id: t\ncapability: openai:chat-completions\noffering_id: o\n" +
		"protocol: paid-job/v1\nprice_default: { amount_wei: \"1\", per_units: 1 }\n" +
		"stacking: { primary: true }\nrunner_compose: { image: img }\n",
	})
	for _, state := range []types.HardwareUnitState{
		types.HardwareUnitRetired,
		types.HardwareUnitSuspended,
	} {
		doc, err := Build(Input{
			EnrollmentID: "host-1",
			Assignments: []types.TemplateAssignment{{
				ID: "unit-a|t", HardwareUnitID: "unit-a", TemplateID: "t",
				MemberEthAddress: "0xa",
				// Still active: the pool took the CARD back, and nothing
				// has withdrawn the placement yet.
				State: types.TemplateAssignmentActive,
			}},
			Hardware: []types.HardwareUnit{{
				ID: "unit-a", GPUUUID: "GPU-a", MemberEthAddress: "0xa", State: state,
			}},
			Catalog: cat,
		})
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}
		if len(doc.Services) != 1 {
			t.Fatalf("state %s: services = %d, want the placement still listed so it can drain", state, len(doc.Services))
		}
		if !doc.Services[0].Draining {
			t.Fatalf("state %s: the placement is not draining — this host keeps serving a card it no longer holds", state)
		}
	}

	// The same placement on a healthy card is untouched.
	doc, err := Build(Input{
		EnrollmentID: "host-1",
		Assignments: []types.TemplateAssignment{{
			ID: "unit-a|t", HardwareUnitID: "unit-a", TemplateID: "t",
			MemberEthAddress: "0xa", State: types.TemplateAssignmentActive,
		}},
		Hardware: []types.HardwareUnit{{
			ID: "unit-a", GPUUUID: "GPU-a", MemberEthAddress: "0xa", State: types.HardwareUnitActive,
		}},
		Catalog: cat,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if doc.Services[0].Draining {
		t.Fatal("a placement on a healthy card was marked draining")
	}
}

// A workload the member has to configure — an adapter in front of their
// own vLLM, or a proxy to a third-party API — must render as a
// passthrough, never as a value.
//
// The pool cannot know that address and must not hold that key: a
// catalog file is reviewed in git and the controller's database is not
// where someone's API credential belongs. So the compose carries
// ${NAME} and docker substitutes it from the member's own .env.
func TestMemberSuppliedConfigIsRenderedAsPassthroughNotValue(t *testing.T) {
	cat := catalogOf(t, map[string]string{"shim.yaml": "" +
		"id: shim\ncapability: openai:chat-completions\noffering_id: o\n" +
		"protocol: paid-job/v1\nprice_default: { amount_wei: \"1\", per_units: 1 }\n" +
		"stacking: { primary: true }\n" +
		"runner_compose:\n" +
		"  image: shim:v1\n" +
		"  env: { POOL_FIXED: yes }\n" +
		"  member_env:\n" +
		"    - name: UPSTREAM_BASE_URL\n" +
		"      description: Where your vLLM or third-party API lives.\n" +
		"      required: true\n" +
		"    - name: UPSTREAM_API_KEY\n" +
		"      description: Credential for that API, if it needs one.\n" +
		"      secret: true\n",
	})
	doc, err := Build(Input{
		EnrollmentID: "host-1",
		Assignments: []types.TemplateAssignment{{
			ID: "unit-a|shim", HardwareUnitID: "unit-a", TemplateID: "shim",
			MemberEthAddress: "0xa", State: types.TemplateAssignmentActive,
		}},
		Hardware: []types.HardwareUnit{{ID: "unit-a", GPUUUID: "GPU-a", MemberEthAddress: "0xa"}},
		Catalog:  cat,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	fragment := doc.Services[0].ComposeFragment

	// Exactly one environment block: two would be duplicate-key YAML
	// and the agent could not load the file at all.
	if got := strings.Count(fragment, "environment:"); got != 1 {
		t.Fatalf("environment blocks = %d, want exactly 1:\n%s", got, fragment)
	}
	for _, want := range []string{
		"POOL_FIXED: yes",
		"UPSTREAM_BASE_URL: ${UPSTREAM_BASE_URL}",
		"UPSTREAM_API_KEY: ${UPSTREAM_API_KEY}",
	} {
		if !strings.Contains(fragment, want) {
			t.Fatalf("fragment is missing %q:\n%s", want, fragment)
		}
	}

	// And the member is told what to set, with the secret marked so a
	// portal can mask it.
	required := doc.Services[0].RequiredEnv
	if len(required) != 2 {
		t.Fatalf("RequiredEnv = %+v, want both names carried to the member", required)
	}
	var sawSecret bool
	for _, v := range required {
		if v.Name == "UPSTREAM_API_KEY" {
			sawSecret = v.Secret
			if v.Description == "" {
				t.Fatal("a secret with no description is a masked field the member cannot act on")
			}
		}
	}
	if !sawSecret {
		t.Fatal("the credential is not marked secret; a portal would render it in the clear")
	}
}
