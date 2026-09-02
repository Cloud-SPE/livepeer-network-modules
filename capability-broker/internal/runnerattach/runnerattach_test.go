package runnerattach

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testKnown() Known {
	ext := map[string]bool{"openai-usage": true, "response-jsonpath": true, "ffmpeg-progress": true, "seconds-elapsed": true}
	return Known{
		Extractor:  func(n string) bool { return ext[n] },
		ProbeTypes: map[string]bool{"http-status": true, "http-jsonpath": true, "http-openai-model-ready": true, "tcp-connect": true},
		Protocols:  map[string]bool{"paid-job/v1": true, "paid-session/v1": true},
		Credential: func(kind, token string) (string, bool, bool) {
			if kind != "bearer" {
				return "", false, false
			}
			return "", strings.HasPrefix(token, "lpc_"), true
		},
	}
}

const examplesDir = "../../../livepeer-network-protocol/protocols/runner-attach/examples"

// The protocol's own examples are the contract: every valid one is
// accepted with every capability accepted; every invalid one is rejected
// at document level (they are all document-level shapes by construction).
func TestProtocolExamples(t *testing.T) {
	valid, _ := filepath.Glob(filepath.Join(examplesDir, "*.json"))
	if len(valid) == 0 {
		t.Skip("protocol examples not present")
	}
	for _, p := range valid {
		raw, _ := os.ReadFile(p)
		doc, res := Evaluate(raw, testKnown())
		if res.Document != "accepted" || doc == nil {
			t.Fatalf("%s: %+v", filepath.Base(p), res)
		}
		for _, c := range res.Capabilities {
			if c.Status != "accepted" {
				t.Fatalf("%s: entry %d rejected: %+v", filepath.Base(p), c.Index, c.Reasons)
			}
		}
		if doc.Credential.Token != "" {
			t.Fatal("token retained on the document")
		}
	}
	invalid, _ := filepath.Glob(filepath.Join(examplesDir, "invalid", "*.json"))
	wantCode := map[string]string{
		"unknown-field-price.json":               "unknown_field",
		"unknown-capability-field-capacity.json": "unknown_field",
		"bad-major.json":                         "contract_version_unsupported",
		"bearer-with-signature.json":             "", // schema-level; broker accepts (signature ignored) — see below
	}
	for _, p := range invalid {
		raw, _ := os.ReadFile(p)
		_, res := Evaluate(raw, testKnown())
		name := filepath.Base(p)
		if code, ok := wantCode[name]; ok && code != "" {
			if res.Document != "rejected" || res.Reasons[0].Code != code {
				t.Fatalf("%s: want %s, got %+v", name, code, res)
			}
			continue
		}
		// The rest are capability-level shapes (extractor on session,
		// missing metering, path without {id}, local probe, bad path) or
		// credential-shape ones: the document may be accepted but the
		// entry must be rejected.
		if res.Document == "rejected" {
			continue
		}
		rejected := false
		for _, c := range res.Capabilities {
			if c.Status == "rejected" {
				rejected = true
			}
		}
		if !rejected && name != "bearer-with-signature.json" {
			t.Fatalf("%s: accepted outright: %+v", name, res)
		}
	}
}

func minimal(mut func(m map[string]any)) []byte {
	m := map[string]any{
		"contract_version": "1.0",
		"credential":       map[string]any{"kind": "bearer", "token": "lpc_x"},
		"host_id":          "h1",
		"agent_version":    "a/1",
		"hardware":         []any{map[string]any{"gpu_uuid": "G1", "gpu_model": "M", "vram_bytes": 8}},
		"capabilities": []any{map[string]any{
			"capability_id": "c", "protocol": "paid-job/v1", "transports": []any{"unary"},
			"work_unit":       map[string]any{"name": "tokens", "extractor": map[string]any{"type": "openai-usage"}},
			"paths":           map[string]any{"invoke": "/v1/x"},
			"readiness":       map[string]any{"type": "http-status"},
			"identity":        map[string]any{"openai.model": "m"},
			"schema_versions": map[string]any{"paid-job/v1": "1.0.15"},
		}},
	}
	if mut != nil {
		mut(m)
	}
	b, _ := json.Marshal(m)
	return b
}

func cap0(m map[string]any) map[string]any { return m["capabilities"].([]any)[0].(map[string]any) }

func TestDocumentLevelRejections(t *testing.T) {
	cases := []struct {
		name, code, field string
		mut               func(map[string]any)
	}{
		{"malformed", "malformed", "", nil},
		{"bad credential", "credential_rejected", "/credential", func(m map[string]any) { m["credential"] = map[string]any{"kind": "bearer", "token": "nope"} }},
		{"kind unsupported", "credential_kind_unsupported", "/credential/kind", func(m map[string]any) { m["credential"] = map[string]any{"kind": "ed25519", "key_id": "k"} }},
		{"unknown host field", "unknown_field", "/price", func(m map[string]any) { m["price"] = 1 }},
		{"unknown nested field", "unknown_field", "/capabilities/0/work_unit/rate", func(m map[string]any) { cap0(m)["work_unit"].(map[string]any)["rate"] = 1 }},
		{"dup gpu", "duplicate_gpu_uuid", "/hardware/1/gpu_uuid", func(m map[string]any) {
			m["hardware"] = append(m["hardware"].([]any), map[string]any{"gpu_uuid": "G1", "gpu_model": "M", "vram_bytes": 8})
		}},
		{"dup identity", "duplicate_capability", "/capabilities/1", func(m map[string]any) {
			dup := map[string]any{}
			for k, v := range cap0(m) {
				dup[k] = v
			}
			dup["local_id"] = "other"
			m["capabilities"] = append(m["capabilities"].([]any), dup)
		}},
		{"dup local id", "duplicate_capability", "/capabilities/1/local_id", func(m map[string]any) {
			dup := map[string]any{}
			for k, v := range cap0(m) {
				dup[k] = v
			}
			dup["identity"] = map[string]any{"openai.model": "other"}
			cap0(m)["local_id"] = "same"
			dup["local_id"] = "same"
			m["capabilities"] = append(m["capabilities"].([]any), dup)
		}},
		{"host mismatch", "host_id_mismatch", "/host_id", func(m map[string]any) { m["host_id"] = "wrong" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var raw []byte
			if tc.name == "malformed" {
				raw = []byte("[1,2]")
			} else {
				raw = minimal(tc.mut)
			}
			known := testKnown()
			if tc.name == "host mismatch" {
				known.Credential = func(_, _ string) (string, bool, bool) { return "h1", true, true }
			}
			doc, res := Evaluate(raw, known)
			if doc != nil || res.Document != "rejected" || len(res.Reasons) == 0 || res.Reasons[0].Code != tc.code || res.Reasons[0].Field != tc.field {
				t.Fatalf("got %+v", res)
			}
			if len(res.Capabilities) != 0 {
				t.Fatal("capabilities on a rejected document")
			}
		})
	}
}

func TestCapabilityLevelRejectionsKeepOthers(t *testing.T) {
	raw := minimal(func(m map[string]any) {
		bad := map[string]any{
			"capability_id": "c2", "protocol": "paid-job/v1", "transports": []any{"unary"},
			"work_unit":       map[string]any{"name": "tokens", "extractor": map[string]any{"type": "no-such"}},
			"paths":           map[string]any{"invoke": "/v1/x"},
			"readiness":       map[string]any{"type": "command-exit-0"},
			"identity":        map[string]any{},
			"schema_versions": map[string]any{"paid-job/v1": "2.0.0"},
			"requirements":    map[string]any{"gpu_vram_min_bytes": 1 << 40},
			"devices":         []any{"G9"},
		}
		m["capabilities"] = append(m["capabilities"].([]any), bad)
	})
	doc, res := Evaluate(raw, testKnown())
	if res.Document != "accepted" || len(doc.Capabilities) != 1 || len(res.Capabilities) != 2 {
		t.Fatalf("got %+v", res)
	}
	if res.Capabilities[0].Status != "accepted" || res.Capabilities[1].Status != "rejected" {
		t.Fatalf("statuses %+v", res.Capabilities)
	}
	codes := map[string]bool{}
	for _, r := range res.Capabilities[1].Reasons {
		codes[r.Code] = true
		if r.Declared == "" && r.Code != "requirements_unmet" {
			t.Fatalf("reason without declared: %+v", r)
		}
	}
	for _, want := range []string{"extractor_unknown", "readiness_type_unknown", "schema_version_major_mismatch", "requirements_unmet", "device_unknown"} {
		if !codes[want] {
			t.Fatalf("missing %s in %v", want, codes)
		}
	}
	if doc.Capabilities[0].LocalID != "0" {
		t.Fatalf("local_id default = %q", doc.Capabilities[0].LocalID)
	}
}

func TestSessionRules(t *testing.T) {
	raw := minimal(func(m map[string]any) {
		m["capabilities"] = []any{map[string]any{
			"capability_id": "s", "protocol": "paid-session/v1", "descriptor_schemas": []any{"sfu-room/v1"},
			"metering":        "runner-reported",
			"work_unit":       map[string]any{"name": "participant_seconds"},
			"paths":           map[string]any{"create": "/s", "status": "/s/{id}", "terminate": "/s/{id}"},
			"readiness":       map[string]any{"type": "http-status", "path": "/ready"},
			"identity":        map[string]any{"provider": "x"},
			"schema_versions": map[string]any{"paid-session/v1": "1.0.11", "sfu-room/v1": "1.0.0"},
		}}
	})
	doc, res := Evaluate(raw, testKnown())
	if res.Document != "accepted" || len(doc.Capabilities) != 1 {
		t.Fatalf("session accept: %+v", res)
	}
	// missing schema version for the descriptor, path without {id}
	raw = minimal(func(m map[string]any) {
		m["capabilities"] = []any{map[string]any{
			"capability_id": "s", "protocol": "paid-session/v1", "descriptor_schemas": []any{"sfu-room/v1"},
			"metering":        "runner-reported",
			"work_unit":       map[string]any{"name": "participant_seconds"},
			"paths":           map[string]any{"create": "/s", "status": "/s/status", "terminate": "/s/{id}"},
			"readiness":       map[string]any{"type": "http-status"},
			"identity":        map[string]any{},
			"schema_versions": map[string]any{"paid-session/v1": "1.0.11"},
		}}
	})
	_, res = Evaluate(raw, testKnown())
	codes := map[string]bool{}
	for _, r := range res.Capabilities[0].Reasons {
		codes[r.Code] = true
	}
	if !codes["schema_version_missing"] || !codes["path_invalid"] {
		t.Fatalf("codes %v", codes)
	}
}

// A descriptor schema the broker has never heard of is a schema it can
// carry: it never interprets the body, so the only check is that the
// runner versions the tag. Before 2026-09-02 a closed list made every
// new schema a broker release, which is the cost plan 0045 removes for
// capabilities and decision 5 removes here.
func TestUnlistedDescriptorSchemaIsCarriedWhenVersioned(t *testing.T) {
	session := func(tag string, versioned bool) []byte {
		return minimal(func(m map[string]any) {
			sv := map[string]any{"paid-session/v1": "1.0.11"}
			if versioned {
				sv[tag] = "1.0.0"
			}
			m["capabilities"] = []any{map[string]any{
				"capability_id": "audio:transcribe.live", "protocol": "paid-session/v1",
				"descriptor_schemas": []any{tag},
				"metering":           "runner-reported",
				"work_unit":          map[string]any{"name": "audio_seconds"},
				"paths":              map[string]any{"create": "/s", "status": "/s/{id}", "terminate": "/s/{id}"},
				"readiness":          map[string]any{"type": "http-status", "path": "/ready"},
				"identity":           map[string]any{"model": "m"},
				"schema_versions":    sv,
			}}
		})
	}
	doc, res := Evaluate(session("pcm-transcript/v1", true), testKnown())
	if res.Document != "accepted" || len(doc.Capabilities) != 1 || len(res.Capabilities[0].Reasons) != 0 {
		t.Fatalf("a versioned, well-formed tag must be carried: %+v", res)
	}
	_, res = Evaluate(session("pcm-transcript/v1", false), testKnown())
	codes := map[string]bool{}
	for _, r := range res.Capabilities[0].Reasons {
		codes[r.Code] = true
	}
	if !codes["schema_version_missing"] || codes["descriptor_schema_unknown"] {
		t.Fatalf("an unversioned tag is schema_version_missing and nothing else: %v", codes)
	}
	_, res = Evaluate(session("pcm transcript", true), testKnown())
	codes = map[string]bool{}
	for _, r := range res.Capabilities[0].Reasons {
		codes[r.Code] = true
	}
	if !codes["schema_violation"] {
		t.Fatalf("a malformed tag is still a schema_violation: %v", codes)
	}
}

func TestProjectionAndDiff(t *testing.T) {
	doc, _ := Evaluate(minimal(func(m map[string]any) {
		cap0(m)["x-quant"] = "fp8"
		cap0(m)["transports"] = []any{"stream", "unary"}
	}), testKnown())
	p := Project(&doc.Capabilities[0], []string{"x-quant", "x-absent"})
	canon, hash, err := p.Canonical()
	if err != nil || !strings.HasPrefix(hash, "sha256:") {
		t.Fatal(err)
	}
	if !strings.Contains(string(canon), `"transports":["stream","unary"]`) || !strings.Contains(string(canon), `"paid-job/v1":"1"`) ||
		!strings.Contains(string(canon), `"promoted":{"x-quant":"fp8"}`) {
		t.Fatalf("canonical %s", canon)
	}
	// Same shape, different minor and different key order → identical hash.
	doc2, _ := Evaluate(minimal(func(m map[string]any) {
		cap0(m)["x-quant"] = "fp8"
		cap0(m)["transports"] = []any{"unary", "stream"}
		cap0(m)["schema_versions"] = map[string]any{"paid-job/v1": "1.2.0"}
		cap0(m)["paths"] = map[string]any{"invoke": "/elsewhere"}
	}), testKnown())
	_, hash2, _ := Project(&doc2.Capabilities[0], []string{"x-quant"}).Canonical()
	if hash2 != hash {
		t.Fatalf("hash differs for equal shape: %s vs %s", hash, hash2)
	}
	// A changed identity is named.
	doc3, _ := Evaluate(minimal(func(m map[string]any) { cap0(m)["identity"] = map[string]any{"openai.model": "other"} }), testKnown())
	d := Diff(p, Project(&doc3.Capabilities[0], nil))
	if len(d) == 0 || d[0].Field != "/identity/openai.model" || d[0].Declared != `"other"` || d[0].Expected != `"m"` {
		t.Fatalf("diff %+v", d)
	}
}

// The reference agent's real output must be accepted by the reference
// broker. Both sides are written against the spec and never import each
// other, so this is the only place the two implementations actually
// meet — a profile that drifts from the contract fails here instead of
// at a member's first attach.
func TestReferenceAgentDocumentsAreAccepted(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "pool-member-agent", "testdata", "attach")
	docs, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	if len(docs) == 0 {
		t.Skip("pool-member-agent goldens not present")
	}
	known := testKnown()
	// The agent's profiles name real extractors and probes; the fake
	// registry above knows only a few, so widen it to what the broker
	// actually ships for this check.
	for _, name := range []string{"multipart-audio-duration", "request-formula", "bytes-counted"} {
		n := name
		prev := known.Extractor
		known.Extractor = func(x string) bool { return x == n || prev(x) }
	}
	for _, path := range docs {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			doc, res := Evaluate(raw, known)
			if res.Document != "accepted" || doc == nil {
				t.Fatalf("agent document rejected: %+v", res.Reasons)
			}
			for _, c := range res.Capabilities {
				if c.Status != "accepted" {
					t.Fatalf("agent capability %q rejected: %+v", c.LocalID, c.Reasons)
				}
			}
		})
	}
}
