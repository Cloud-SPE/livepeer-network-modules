package desiredstate

import (
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func vendorTmpl() templates.Template {
	return templates.Template{
		ID: "t", Capability: "video:transcode.vod", Protocol: "paid-job/v1",
		RunnerCompose: templates.RunnerCompose{Image: map[string]string{
			"nvidia": "x/vod-nvidia:1",
			"intel":  "x/vod-intel:1",
		}},
	}
}

// One template, two builds: the card decides which image is rendered
// and how the card reaches the container. NVIDIA pins the one card by
// UUID; Intel exposes only its assigned DRI render node.
func TestRenderComposePicksTheVendorsImageAndDeviceBlock(t *testing.T) {
	nvidia := renderCompose("svc", vendorTmpl(), types.HardwareUnit{
		GPUModel: "NVIDIA GeForce RTX 4090", GPUUUID: "GPU-abc",
	}, nil)
	if !strings.Contains(nvidia, "image: x/vod-nvidia:1") {
		t.Fatalf("NVIDIA card did not get the nvidia image:\n%s", nvidia)
	}
	if !strings.Contains(nvidia, `device_ids: ["GPU-abc"]`) || !strings.Contains(nvidia, "driver: nvidia") {
		t.Fatalf("NVIDIA card is not pinned by UUID:\n%s", nvidia)
	}
	if strings.Contains(nvidia, "/dev/dri") {
		t.Fatalf("NVIDIA card rendered Intel's device exposure:\n%s", nvidia)
	}

	intel := renderCompose("svc", vendorTmpl(), types.HardwareUnit{
		GPUModel: "Intel(R) Arc(TM) A770 Graphics", GPUUUID: "GPU-def", RuntimeFacts: map[string]string{"render_node": "/dev/dri/renderD130", "render_node_gid": "109"},
	}, nil)
	if !strings.Contains(intel, "image: x/vod-intel:1") {
		t.Fatalf("Intel card did not get the intel image:\n%s", intel)
	}
	if !strings.Contains(intel, `- "/dev/dri/renderD130:/dev/dri/renderD128"`) {
		t.Fatalf("Intel card was not given the DRI nodes:\n%s", intel)
	}
	if strings.Contains(intel, "device_ids") || strings.Contains(intel, "driver: nvidia") {
		t.Fatalf("Intel card rendered NVIDIA's runtime reservation, which its host does not have:\n%s", intel)
	}
}

// A template with no runner_compose renders no image line, as before —
// placement is what refuses a card the template has no build for, and
// the renderer never invents one.
func TestRenderComposeWithNoImageRendersNoImageLine(t *testing.T) {
	out := renderCompose("svc", templates.Template{ID: "t"}, types.HardwareUnit{
		GPUModel: "NVIDIA GeForce RTX 4090", GPUUUID: "GPU-abc",
	}, nil)
	if strings.Contains(out, "image:") {
		t.Fatalf("an image-less template rendered an image line:\n%s", out)
	}
}

// member_env is gone with the case it existed for. Nothing in a
// rendered fragment may ask the member's host for a value any more.
func TestRenderComposeCarriesNoMemberPassthrough(t *testing.T) {
	tmpl := vendorTmpl()
	tmpl.RunnerCompose.Env = map[string]string{"MODEL": "m", "PORT": "8080"}
	out := renderCompose("svc", tmpl, types.HardwareUnit{GPUModel: "NVIDIA GeForce RTX 4090", GPUUUID: "GPU-abc"}, nil)
	if strings.Contains(out, "${") {
		t.Fatalf("fragment still asks the member's host for a value:\n%s", out)
	}
	if !strings.Contains(out, `MODEL: "m"`) || !strings.Contains(out, `PORT: "8080"`) {
		t.Fatalf("pool-set env was dropped with the passthrough:\n%s", out)
	}
}

func TestIntelBuildRequiresSpecificDeviceInventory(t *testing.T) {
	cat := catalogOf(t, map[string]string{"t.yaml": templateYAML("intel", "intel", "runner:1")})
	for _, facts := range []map[string]string{nil, {"render_node": "/dev/dri"}, {"render_node": "/dev/dri/renderD128"}, {"render_node": "/dev/dri/renderD128", "render_node_gid": "bad"}} {
		hw := unit("a", "intel-host-a")
		hw.GPUModel = "Intel Arc A770"
		hw.RuntimeFacts = facts
		_, err := Build(Input{EnrollmentID: "host", Catalog: cat, Hardware: []types.HardwareUnit{hw}, Assignments: []types.TemplateAssignment{assignment("a", "intel", types.TemplateAssignmentActive)}})
		if err == nil {
			t.Fatalf("unsafe Intel inventory accepted: %v", facts)
		}
	}
	hw := unit("a", "intel-host-a")
	hw.GPUModel = "Intel Arc A770"
	hw.RuntimeFacts = map[string]string{"render_node": "/dev/dri/renderD130", "render_node_gid": "109"}
	doc, err := Build(Input{EnrollmentID: "host", Catalog: cat, Hardware: []types.HardwareUnit{hw}, Assignments: []types.TemplateAssignment{assignment("a", "intel", types.TemplateAssignmentActive)}})
	if err != nil {
		t.Fatal(err)
	}
	fragment := doc.Services[0].ComposeFragment
	if strings.Contains(fragment, "/dev/dri:/dev/dri") || !strings.Contains(fragment, `group_add:
      - "109"`) {
		t.Fatal(fragment)
	}
}
