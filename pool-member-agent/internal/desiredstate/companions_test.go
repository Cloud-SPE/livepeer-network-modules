package desiredstate

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCompanionsRemainWhileDrainingAndStopAsOneAssignment(t *testing.T) {
	doc := Document{EnrollmentID: "host-a", Services: []Service{
		{Name: "runner-chat", Draining: true, ComposeFragment: "  runner-chat:\n    image: proxy:1\n  runner-chat-engine:\n    image: engine:1\n", CacheVolumes: []string{"runner-chat-cache-models"}},
		{Name: "runner-audio", ComposeFragment: "  runner-audio:\n    image: audio:1\n", CacheVolumes: []string{"runner-audio-cache-models"}},
	}}
	running := RenderCompose(doc)
	for _, name := range []string{"runner-chat:", "runner-chat-engine:", "runner-audio:", "runner-chat-cache-models: {}"} {
		if !strings.Contains(running, name) {
			t.Fatal(running)
		}
	}
	doc.Services[0].Stop = true
	stopped := RenderCompose(doc)
	if strings.Contains(stopped, "runner-chat") || !strings.Contains(stopped, "runner-audio-cache-models: {}") {
		t.Fatal(stopped)
	}
	doc.Services[0].Stop = false
	if RenderCompose(doc) != running {
		t.Fatal("restarted assignment lost stable cache names")
	}
}

func TestControllerRegionalDocumentComposeGolden(t *testing.T) {
	raw, err := os.ReadFile("../../../pool-controller/testdata/regional-runners.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc Document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	rendered := RenderCompose(doc)
	path := "../../testdata/regional-runners.compose.yaml"
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, []byte(rendered), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if rendered != string(want) {
		t.Fatal("controller/agent regional Compose drifted")
	}
	if len(doc.Services) != 3 || strings.Count(rendered, "device_ids:") != 3 || strings.Count(rendered, "-engine:\n") != 1 {
		t.Fatal(rendered)
	}
}
