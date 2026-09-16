package desiredstate

import (
	"bytes"
	"encoding/json"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"gopkg.in/yaml.v3"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestCompanionGroupPinsOnlyEngineAndPersistsAssignmentCache(t *testing.T) {
	cat := catalogOf(t, map[string]string{"chat.yaml": templateYAML("chat", "chat", "proxy:1") + `  gpu: false
  caches: {metadata: /metadata}
  companions:
    - name: engine
      image: {nvidia: engine:1}
      gpu: true
      command: ["--model", "a: b", "--literal", "${HOST_SECRET}"]
      env: {PRIMARY: "http://{{service.main}}:8080"}
      caches: {models: /models}
`})
	tmpl, _ := cat.Get("chat")
	tmpl.RunnerCompose.Env["UPSTREAM_URL"] = "http://{{service.engine}}:8000"
	fragment := renderCompose("runner-a", tmpl, unit("a", "GPU-aaa"), nil)
	var parsed struct {
		Services map[string]struct {
			Image       string
			Environment map[string]string
			Command     []string
			Deploy      any
			DependsOn   []string `yaml:"depends_on"`
			Volumes     []struct{ Source, Target string }
		}
	}
	if err := yaml.Unmarshal([]byte("services:\n"+fragment), &parsed); err != nil {
		t.Fatal(err)
	}
	main, engine := parsed.Services["runner-a"], parsed.Services["runner-a-engine"]
	if len(parsed.Services) != 2 || main.Deploy != nil || engine.Deploy == nil || main.Environment["UPSTREAM_URL"] != "http://runner-a-engine:8000" || engine.Environment["PRIMARY"] != "http://runner-a:8080" {
		t.Fatalf("invalid group: %s", fragment)
	}
	if !reflect.DeepEqual(main.DependsOn, []string{"runner-a-engine"}) || !reflect.DeepEqual(engine.Command, []string{"--model", "a: b", "--literal", "$${HOST_SECRET}"}) {
		t.Fatalf("literal or dependency changed: %s", fragment)
	}
	if engine.Volumes[0].Source != "runner-a-cache-models" || engine.Volumes[0].Target != "/models" || strings.Count(fragment, "GPU-aaa") != 1 {
		t.Fatal(fragment)
	}
	if got := cacheVolumes("runner-a", tmpl); !reflect.DeepEqual(got, []string{"runner-a-cache-metadata", "runner-a-cache-models"}) {
		t.Fatal(got)
	}
	doc, err := Build(Input{EnrollmentID: "host-1", Catalog: cat, Assignments: []types.TemplateAssignment{assignment("a", "chat", types.TemplateAssignmentActive)}, Hardware: []types.HardwareUnit{unit("a", "GPU-aaa")}})
	if err != nil || len(doc.Services) != 1 || len(doc.Services[0].CacheVolumes) != 2 {
		t.Fatalf("one assignment must attach once: %+v %v", doc, err)
	}
}

// Shared with the member-agent test: the serialized controller document is the
// boundary, including companion containers and top-level named cache declarations.
func TestRegionalRunnerDocumentGolden(t *testing.T) {
	cat, err := templates.Load("../../../templates")
	if err != nil {
		t.Fatal(err)
	}
	var assignments []types.TemplateAssignment
	for _, id := range []string{"openai-chat-qwen3.6-27b", "openai-audio-speech-kokoro", "openai-audio-transcriptions-whisper-large-v3"} {
		assignments = append(assignments, assignment("unit-a", id, types.TemplateAssignmentActive))
	}
	hardware := unit("unit-a", "GPU-regional-5090")
	hardware.GPUModel = "NVIDIA GeForce RTX 5090"
	doc, err := Build(Input{EnrollmentID: "regional-runner-fixture", Catalog: cat, Assignments: assignments, Hardware: []types.HardwareUnit{hardware}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	path := "../../testdata/regional-runners.json"
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, raw, 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, want) {
		t.Fatal("regional runner controller document drifted")
	}
}

func TestControllerSendsSecretNamesAndPlaceholdersOnly(t *testing.T) {
	cat := catalogOf(t, map[string]string{"live.yaml": templateYAML("live", "live", "runner:1") + "  secret_env: [MASTER_KEY, BROKER_TOKEN]\n  local_bearer_env: BROKER_TOKEN\n"})
	doc, err := Build(Input{EnrollmentID: "host-1", Catalog: cat, Assignments: []types.TemplateAssignment{assignment("a", "live", types.TemplateAssignmentActive)}, Hardware: []types.HardwareUnit{unit("a", "GPU-a")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Services) != 1 {
		t.Fatalf("services: %+v", doc.Services)
	}
	svc := doc.Services[0]
	if !reflect.DeepEqual(svc.SecretEnv, []string{"MASTER_KEY", "BROKER_TOKEN"}) || svc.LocalBearerEnv != "BROKER_TOKEN" || !strings.Contains(svc.ComposeFragment, `MASTER_KEY: "{{secret.MASTER_KEY}}"`) || !strings.Contains(svc.ComposeFragment, `BROKER_TOKEN: "{{secret.BROKER_TOKEN}}"`) {
		t.Fatalf("secret declarations lost: %+v", svc)
	}
}
