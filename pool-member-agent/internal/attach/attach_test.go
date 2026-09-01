package attach

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testHost() Host {
	return Host{
		HostID: "host-1", AgentVersion: "pool-member-agent/test",
		Credential: Credential{Kind: "bearer", Token: "lpc_test"},
		Hardware: []Hardware{{
			GPUUUID: "GPU-8f3c2a1e-4b7d-4e9f-a0c1-2d3e4f5a6b7c", GPUModel: "NVIDIA GeForce RTX 4090",
			VRAMBytes: 24 << 30,
		}},
	}
}

// chatContract is a real runner's contract, x-* extension included.
const chatContract = `{
  "capability_id": "openai:chat-completions",
  "protocol": "paid-job/v1",
  "transports": ["unary", "stream"],
  "work_unit": {"name": "tokens", "extractor": {"type": "openai-usage", "field": "total_tokens"}},
  "paths": {"invoke": "/v1/chat/completions"},
  "readiness": {"type": "http-openai-model-ready", "path": "/v1/models", "config": {"model": "llama-3-70b"}},
  "identity": {"openai.model": "llama-3-70b", "provider": "vllm"},
  "schema_versions": {"paid-job/v1": "1.0.15"},
  "requirements": {"gpu_vram_min_bytes": 68719476736},
  "x-quantization": "fp8"
}`

const liveContract = `{
  "capability_id": "video:transcode.live",
  "protocol": "paid-session/v1",
  "descriptor_schemas": ["rtmp-hls/v1"],
  "metering": "runner-reported",
  "work_unit": {"name": "output_seconds"},
  "paths": {"create": "/v1/video/live/sessions", "status": "/v1/video/live/sessions/{id}", "terminate": "/v1/video/live/sessions/{id}"},
  "readiness": {"type": "http-status", "path": "/healthz"},
  "identity": {"provider": "livepeer-live-runner"},
  "schema_versions": {"paid-session/v1": "1.0.0", "rtmp-hls/v1": "1.0.0"}
}`

// serveContract is a runner: it answers its well-known path and nothing
// else, which is all the agent asks of it.
func serveContract(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ContractPath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mustResolve(t *testing.T, runners ...Runner) []Resolved {
	t.Helper()
	resolved, errs := Resolve(context.Background(), nil, runners)
	for _, err := range errs {
		t.Fatalf("resolve: %v", err)
	}
	return resolved
}

// contract-relayed-verbatim: every runner-owned field of the served
// contract appears unchanged, and the agent's three fields are the
// agent's.
func TestContractIsRelayedVerbatim(t *testing.T) {
	srv := serveContract(t, chatContract, http.StatusOK)
	doc, err := Build(testHost(), mustResolve(t, Runner{
		LocalID: "chat", URL: srv.URL,
		Devices: []string{"GPU-8f3c2a1e-4b7d-4e9f-a0c1-2d3e4f5a6b7c", "GPU-not-on-this-host"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Capabilities) != 1 {
		t.Fatalf("capabilities = %d", len(doc.Capabilities))
	}
	c := doc.Capabilities[0]
	if c.CapabilityID != "openai:chat-completions" || c.Protocol != "paid-job/v1" {
		t.Fatalf("capability/protocol = %s/%s", c.CapabilityID, c.Protocol)
	}
	if strings.Join(c.Transports, ",") != "unary,stream" {
		t.Fatalf("transports = %v", c.Transports)
	}
	if c.WorkUnit.Name != "tokens" || c.WorkUnit.Extractor["type"] != "openai-usage" {
		t.Fatalf("work_unit = %+v", c.WorkUnit)
	}
	if c.Paths["invoke"] != "/v1/chat/completions" || c.Readiness.Type != "http-openai-model-ready" {
		t.Fatalf("paths/readiness = %v / %+v", c.Paths, c.Readiness)
	}
	if c.Identity["openai.model"] != "llama-3-70b" || c.Identity["provider"] != "vllm" {
		t.Fatalf("identity = %v", c.Identity)
	}
	if c.Requirements == nil || c.Requirements.GPUVRAMMinBytes != 68719476736 {
		t.Fatalf("requirements = %+v", c.Requirements)
	}
	if c.Extensions["x-quantization"] != "fp8" {
		t.Fatalf("extensions = %v", c.Extensions)
	}
	// The agent's own three.
	if c.LocalID != "chat" {
		t.Fatalf("local_id = %q", c.LocalID)
	}
	if len(c.Devices) != 1 || c.Devices[0] != "GPU-8f3c2a1e-4b7d-4e9f-a0c1-2d3e4f5a6b7c" {
		t.Fatalf("devices = %v; a device not in hardware[] must be dropped", c.Devices)
	}
	// And x-* survives serialisation at the top level of the entry.
	raw, _ := json.Marshal(c)
	if !strings.Contains(string(raw), `"x-quantization":"fp8"`) {
		t.Fatalf("extension not serialised: %s", raw)
	}
}

// contract-missing-omits-not-fails: one image that does not adhere must
// not keep the rest of the host from attaching.
func TestMissingContractIsNamedAndOmitted(t *testing.T) {
	good := serveContract(t, chatContract, http.StatusOK)
	bad := serveContract(t, `{"error":"no such path"}`, http.StatusNotFound)
	resolved, errs := Resolve(context.Background(), nil, []Runner{
		{LocalID: "chat", URL: good.URL},
		{LocalID: "vod", URL: bad.URL},
	})
	if len(resolved) != 1 || resolved[0].Runner.LocalID != "chat" {
		t.Fatalf("resolved = %+v, want only chat", resolved)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want one, for vod", errs)
	}
	msg := errs[0].Error()
	// The operator has to be able to act on this line alone.
	for _, want := range []string{`"vod"`, bad.URL, ContractPath, "404", "runner-contract.md"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error does not name %q: %s", want, msg)
		}
	}
	var re *ResolveError
	if !asResolveError(errs[0], &re) || re.LocalID != "vod" {
		t.Fatalf("error is not a ResolveError for vod: %T", errs[0])
	}
	doc, err := Build(testHost(), resolved)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Capabilities) != 1 {
		t.Fatalf("host attached with %d capabilities, want the one that adhered", len(doc.Capabilities))
	}
}

func asResolveError(err error, target **ResolveError) bool {
	re, ok := err.(*ResolveError)
	if ok {
		*target = re
	}
	return ok
}

// contract-unknown-field-rejected: a misspelled field that decoded to
// nothing would attach a runner that is not what it says it is.
func TestContractRejectsUnknownFieldsButKeepsExtensions(t *testing.T) {
	typo := strings.Replace(chatContract, `"transports"`, `"transport"`, 1)
	srv := serveContract(t, typo, http.StatusOK)
	if _, errs := Resolve(context.Background(), nil, []Runner{{LocalID: "chat", URL: srv.URL}}); len(errs) != 1 ||
		!strings.Contains(errs[0].Error(), `"transport"`) {
		t.Fatalf("a misspelled field was accepted: %v", errs)
	}
	// Whereas an x-* key of any shape is an extension.
	var c Contract
	if err := json.Unmarshal([]byte(`{"capability_id":"a","protocol":"paid-job/v1","paths":{"invoke":"/x"},
		"work_unit":{"name":"u"},"readiness":{"type":"http-status"},"x-anything":{"deep":[1,2]}}`), &c); err != nil {
		t.Fatalf("x-* rejected: %v", err)
	}
	if c.Extensions["x-anything"] == nil {
		t.Fatal("x-* not gathered")
	}
}

// contract-draining-relayed: the field the old profile expansion dropped.
func TestDrainingIsRelayed(t *testing.T) {
	srv := serveContract(t, liveContract, http.StatusOK)
	doc, err := Build(testHost(), mustResolve(t, Runner{LocalID: "live", URL: srv.URL, Draining: true}))
	if err != nil {
		t.Fatal(err)
	}
	if !doc.Capabilities[0].Draining {
		t.Fatal("draining was not relayed")
	}
	raw, _ := json.Marshal(doc)
	if !strings.Contains(string(raw), `"draining":true`) {
		t.Fatalf("draining not on the wire: %s", raw)
	}
	// A session runner's shape came through too.
	c := doc.Capabilities[0]
	if c.Metering != "runner-reported" || len(c.DescriptorSchemas) != 1 || c.Paths["terminate"] == "" {
		t.Fatalf("session fields not relayed: %+v", c)
	}
}

// The agent refuses only what cannot be relayed at all; the broker
// validates the rest and names the field.
func TestContractValidateIsShallow(t *testing.T) {
	cases := map[string]string{
		"no capability": `{"protocol":"paid-job/v1","paths":{"invoke":"/x"},"work_unit":{"name":"u"},"readiness":{"type":"http-status"}}`,
		"no paths":      `{"capability_id":"a","protocol":"paid-job/v1","work_unit":{"name":"u"},"readiness":{"type":"http-status"}}`,
		"no work unit":  `{"capability_id":"a","protocol":"paid-job/v1","paths":{"invoke":"/x"},"readiness":{"type":"http-status"}}`,
	}
	for name, body := range cases {
		var c Contract
		if err := json.Unmarshal([]byte(body), &c); err != nil {
			t.Fatalf("%s: unmarshal: %v", name, err)
		}
		if err := c.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestBuildRejections(t *testing.T) {
	srv := serveContract(t, chatContract, http.StatusOK)
	ok := mustResolve(t, Runner{LocalID: "chat", URL: srv.URL})
	if _, err := Build(Host{}, ok); err == nil || !strings.Contains(err.Error(), "host_id") {
		t.Fatalf("no host id: %v", err)
	}
	h := testHost()
	h.Credential.Token = ""
	if _, err := Build(h, ok); err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("no credential: %v", err)
	}
	dup := mustResolve(t, Runner{LocalID: "chat", URL: srv.URL}, Runner{LocalID: "chat", URL: srv.URL})
	if _, err := Build(testHost(), dup); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate local_id: %v", err)
	}
	bad := mustResolve(t, Runner{LocalID: "has space", URL: srv.URL})
	if _, err := Build(testHost(), bad); err == nil || !strings.Contains(err.Error(), "local_id") {
		t.Fatalf("bad local_id: %v", err)
	}
	if _, err := Build(testHost(), []Resolved{{Runner: Runner{LocalID: "x"}}}); err == nil || !strings.Contains(err.Error(), "no contract") {
		t.Fatalf("unresolved runner: %v", err)
	}
}

func TestRouteTable(t *testing.T) {
	table := RouteTable([]Runner{{LocalID: "chat", URL: "http://vllm:8000/"}, {URL: "http://x:1"}})
	if table["chat"] != "http://vllm:8000" || table["runner-1"] != "http://x:1" {
		t.Fatalf("route table = %v", table)
	}
}

// Goldens are the wire documents a real agent sends for a real
// contract; a change here is a change every broker sees.
func TestGoldenDocuments(t *testing.T) {
	chat := serveContract(t, chatContract, http.StatusOK)
	live := serveContract(t, liveContract, http.StatusOK)
	goldens := map[string][]Runner{
		"openai-chat.json": {{
			LocalID: "chat", URL: chat.URL,
			Devices: []string{"GPU-8f3c2a1e-4b7d-4e9f-a0c1-2d3e4f5a6b7c"},
		}},
		"multi-runner.json": {
			{LocalID: "chat", URL: chat.URL},
			{LocalID: "live", URL: live.URL, Draining: true},
		},
		"hardware-only.json": {},
	}
	for name, runners := range goldens {
		doc, err := Build(testHost(), mustResolve(t, runners...))
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
