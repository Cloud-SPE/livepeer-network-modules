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
