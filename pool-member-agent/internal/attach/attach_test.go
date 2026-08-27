package attach

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testHost() Host {
	return Host{
		HostID: "host-3f9a", AgentVersion: "pool-member-agent/test",
		Credential: Credential{Kind: "bearer", Token: "lpc_testtoken"},
		Hardware: []Hardware{{
			GPUUUID: "GPU-8f3c2a1e-4b7d-4e9f-a0c1-2d3e4f5a6b7c", GPUModel: "NVIDIA H100 80GB HBM3",
			VRAMBytes: 85899345920, Driver: "560.35.03", Facts: map[string]string{"source": "nvidia-smi"},
		}},
	}
}

func TestBuildOpenAIChat(t *testing.T) {
	doc, err := Build(testHost(), []Runner{{
		LocalID: "chat", Profile: ProfileOpenAICompatible, URL: "http://vllm:8000",
		Model: "llama-3-70b", Provider: "vllm",
		Devices:    []string{"GPU-8f3c2a1e-4b7d-4e9f-a0c1-2d3e4f5a6b7c"},
		Extensions: map[string]any{"x-quantization": "fp8"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	c := doc.Capabilities[0]
	if c.CapabilityID != "openai:chat-completions" || c.Protocol != "paid-job/v1" {
		t.Fatalf("capability: %+v", c)
	}
	// The eight facts the operator never types.
	if strings.Join(c.Transports, ",") != "unary,stream" {
		t.Fatalf("transports: %v", c.Transports)
	}
	if c.WorkUnit.Name != "tokens" || c.WorkUnit.Extractor["type"] != "openai-usage" {
		t.Fatalf("work unit: %+v", c.WorkUnit)
	}
	if c.Paths["invoke"] != "/v1/chat/completions" {
		t.Fatalf("paths: %v", c.Paths)
	}
	if c.Readiness.Type != "http-openai-model-ready" || c.Readiness.Config["model"] != "llama-3-70b" {
		t.Fatalf("readiness: %+v", c.Readiness)
	}
	if c.Identity["openai.model"] != "llama-3-70b" || c.Identity["provider"] != "vllm" {
		t.Fatalf("identity: %v", c.Identity)
	}
	if c.SchemaVersions["paid-job/v1"] == "" {
		t.Fatalf("schema_versions: %v", c.SchemaVersions)
	}
	// x-* keys sit at the entry's top level, not nested.
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var flat map[string]any
	_ = json.Unmarshal(raw, &flat)
	if flat["x-quantization"] != "fp8" {
		t.Fatalf("extension not flattened: %s", raw)
	}
	if _, leaked := flat["Extensions"]; leaked {
		t.Fatalf("Extensions leaked as a field: %s", raw)
	}
	// The runner's URL is the agent's business, never the broker's.
	if strings.Contains(string(raw), "vllm:8000") {
		t.Fatalf("local URL leaked into the document: %s", raw)
	}
}

func TestBuildEveryOpenAIFamily(t *testing.T) {
	for _, capID := range OpenAICapabilities() {
		t.Run(capID, func(t *testing.T) {
			doc, err := Build(testHost(), []Runner{{
				LocalID: "r", Profile: ProfileOpenAICompatible, URL: "http://x:1",
				CapabilityID: capID, Model: "m",
			}})
			if err != nil {
				t.Fatal(err)
			}
			c := doc.Capabilities[0]
			if len(c.Transports) == 0 || c.WorkUnit.Name == "" || c.WorkUnit.Extractor == nil {
				t.Fatalf("incomplete entry: %+v", c)
			}
			if !strings.HasPrefix(c.Paths["invoke"], "/") {
				t.Fatalf("invoke path must be relative: %q", c.Paths["invoke"])
			}
			// Multipart families must not claim a JSON-only extractor.
			if c.Transports[0] == "multipart" && c.WorkUnit.Extractor["type"] == "openai-usage" {
				t.Fatalf("%s: multipart endpoint cannot read a usage block", capID)
			}
		})
	}
}

func TestBuildTranscode(t *testing.T) {
	doc, err := Build(testHost(), []Runner{{
		LocalID: "abr", Profile: ProfileTranscode, URL: "http://ffmpeg:8080", Model: "h264",
	}})
	if err != nil {
		t.Fatal(err)
	}
	c := doc.Capabilities[0]
	if c.CapabilityID != "video:transcode.abr" || c.Transports[0] != "multipart" {
		t.Fatalf("capability: %+v", c)
	}
	if c.WorkUnit.Extractor["type"] != "ffmpeg-progress" || c.WorkUnit.Extractor["unit"] != "out_time_seconds" {
		t.Fatalf("extractor: %+v", c.WorkUnit.Extractor)
	}
	if c.Identity["codec"] != "h264" {
		t.Fatalf("identity: %v", c.Identity)
	}
}

func TestBuildRejections(t *testing.T) {
	cases := []struct {
		name    string
		host    Host
		runners []Runner
		want    string
	}{
		{"no credential", Host{HostID: "h"}, nil, "credential"},
		{"no host id", Host{Credential: Credential{Token: "t"}}, nil, "host_id"},
		{"unknown profile", testHost(), []Runner{{LocalID: "a", Profile: "magic"}}, "unknown profile"},
		{"missing profile", testHost(), []Runner{{LocalID: "a"}}, "profile is required"},
		{"openai without model", testHost(), []Runner{{LocalID: "a", Profile: ProfileOpenAICompatible}}, "model is required"},
		{"unknown openai endpoint", testHost(), []Runner{{LocalID: "a", Profile: ProfileOpenAICompatible, Model: "m", CapabilityID: "openai:nope"}}, "not an openai-compatible endpoint"},
		{"duplicate local id", testHost(), []Runner{
			{LocalID: "a", Profile: ProfileTranscode}, {LocalID: "a", Profile: ProfileTranscode},
		}, "duplicate local_id"},
		{"bad local id", testHost(), []Runner{{LocalID: "a b", Profile: ProfileTranscode}}, "local_id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Build(tc.host, tc.runners)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Build() = %v, want error containing %q", err, tc.want)
			}
		})
	}
}

// A device the host never reported would earn a device_unknown
// rejection from the broker; the agent drops it instead.
func TestBuildDropsUnreportedDevices(t *testing.T) {
	doc, err := Build(testHost(), []Runner{{
		LocalID: "chat", Profile: ProfileOpenAICompatible, Model: "m",
		Devices: []string{"GPU-8f3c2a1e-4b7d-4e9f-a0c1-2d3e4f5a6b7c", "GPU-does-not-exist"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := doc.Capabilities[0].Devices
	if len(got) != 1 || got[0] != "GPU-8f3c2a1e-4b7d-4e9f-a0c1-2d3e4f5a6b7c" {
		t.Fatalf("devices: %v", got)
	}
}

func TestRouteTable(t *testing.T) {
	rt := RouteTable([]Runner{
		{LocalID: "chat", URL: "http://vllm:8000/"},
		{Profile: ProfileTranscode, URL: "http://ffmpeg:8080"},
	})
	if rt["chat"] != "http://vllm:8000" || rt["runner-1"] != "http://ffmpeg:8080" {
		t.Fatalf("route table: %v", rt)
	}
}

// The goldens are what the agent actually puts on the wire. They are
// validated against the protocol's own JSON Schema by
// `make check-attach-docs`, so a profile that drifts from the contract
// fails there rather than at a broker.
func TestGoldenDocuments(t *testing.T) {
	goldens := map[string][]Runner{
		"openai-chat.json": {{
			LocalID: "chat", Profile: ProfileOpenAICompatible, URL: "http://vllm:8000",
			Model: "llama-3-70b", Provider: "vllm",
			Devices:      []string{"GPU-8f3c2a1e-4b7d-4e9f-a0c1-2d3e4f5a6b7c"},
			Requirements: &Requirements{GPUVRAMMinBytes: 68719476736},
			Extensions:   map[string]any{"x-quantization": "fp8"},
		}},
		"multi-runner.json": {
			{LocalID: "chat", Profile: ProfileOpenAICompatible, URL: "http://vllm:8000", Model: "llama-3-8b"},
			{LocalID: "whisper", Profile: ProfileOpenAICompatible, URL: "http://whisper:9000",
				CapabilityID: "openai:audio-transcriptions", Model: "whisper-large-v3"},
			{LocalID: "abr", Profile: ProfileTranscode, URL: "http://ffmpeg:8080"},
		},
		"hardware-only.json": {},
	}
	for name, runners := range goldens {
		doc, err := Build(testHost(), runners)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, '\n')
		path := filepath.Join("..", "..", "testdata", "attach", name)
		if os.Getenv("UPDATE_GOLDEN") == "1" {
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run UPDATE_GOLDEN=1 go test ./... to write)", name, err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s drifted:\n got: %s\nwant: %s", name, got, want)
		}
	}
}
