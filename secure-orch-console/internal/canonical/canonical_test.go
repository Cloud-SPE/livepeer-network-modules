package canonical

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBytes_DeterministicAcrossOrder(t *testing.T) {
	a := map[string]any{
		"capabilities": []any{
			map[string]any{
				"capability_id": "openai:chat-completions",
				"extra":         json.RawMessage(`{"b":2,"a":1}`),
			},
		},
	}
	b := map[string]any{
		"capabilities": []any{
			map[string]any{
				"extra":         json.RawMessage(`{"a":1,"b":2}`),
				"capability_id": "openai:chat-completions",
			},
		},
	}
	ca, err := Bytes(a)
	if err != nil {
		t.Fatalf("Bytes(a): %v", err)
	}
	cb, err := Bytes(b)
	if err != nil {
		t.Fatalf("Bytes(b): %v", err)
	}
	if string(ca) != string(cb) {
		t.Fatalf("canonical bytes differ:\n  a=%s\n  b=%s", ca, cb)
	}
}

func TestBytes_KeysSorted(t *testing.T) {
	m := map[string]any{
		"z": 1,
		"a": 2,
		"m": map[string]any{"y": 1, "b": 2},
	}
	c, err := Bytes(m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(c), `{"a":2,"m":{"b":2,"y":1},"z":1}`) {
		t.Fatalf("expected sorted keys, got %s", c)
	}
}

func TestBytes_NoWhitespace(t *testing.T) {
	m := map[string]any{"a": []any{1, 2, 3}}
	c, err := Bytes(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(string(c), " \t\n") {
		t.Fatalf("canonical bytes contain whitespace: %q", c)
	}
}

func TestBytes_PreservesNumberPrecision(t *testing.T) {
	// JSON's safe-integer range tops out at 2^53-1. Numbers above that
	// get mangled if we round-trip through float64. UseNumber preserves
	// the exact decimal string.
	raw := []byte(`{"price_per_unit_wei":"99999999999999999999"}`)
	c, err := BytesFromJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(c), `"99999999999999999999"`) {
		t.Fatalf("number mangled: %s", c)
	}
}

func TestBytes_ManifestRoundTrip(t *testing.T) {
	// Realistic shape: outer envelope's inner manifest payload, the
	// canonical-bytes input the cold key signs.
	manifest := map[string]any{
		"spec_version":    "0.2.0",
		"publication_seq": 7,
		"issued_at":       "2026-05-06T12:34:56Z",
		"expires_at":      "2026-06-05T12:34:56Z",
		"orch": map[string]any{
			"eth_address": "0x1234567890abcdef1234567890abcdef12345678",
		},
		"capabilities": []any{
			map[string]any{
				"capability_id":      "openai:chat-completions:llama-3-70b",
				"offering_id":        "vllm-h100-batch4",
				"interaction_mode":   "http-stream@v1",
				"work_unit":          map[string]any{"name": "tokens"},
				"price_per_unit_wei": "1500000",
				"worker_url":         "https://broker-a.example.com",
				"extra":              map[string]any{"region": "us-west-2", "gpu_class": "h100"},
			},
		},
	}
	c1, err := Bytes(manifest)
	if err != nil {
		t.Fatal(err)
	}
	// Round-trip: encode canonical bytes, re-canonicalize, expect same
	// bytes.
	c2, err := BytesFromJSON(c1)
	if err != nil {
		t.Fatal(err)
	}
	if string(c1) != string(c2) {
		t.Fatalf("round-trip differs:\n  c1=%s\n  c2=%s", c1, c2)
	}
	// Sanity check: keys appear in lexicographic order at top level.
	want := []string{
		`"capabilities":`,
		`"expires_at":`,
		`"issued_at":`,
		`"orch":`,
		`"publication_seq":`,
		`"spec_version":`,
	}
	got := string(c1)
	last := -1
	for _, w := range want {
		idx := strings.Index(got, w)
		if idx < 0 {
			t.Fatalf("missing %s in %s", w, got)
		}
		if idx < last {
			t.Fatalf("keys out of order: expected %s after position %d, got %d in %s", w, last, idx, got)
		}
		last = idx
	}
}

func TestSHA256Hex_Stable(t *testing.T) {
	c := []byte(`{"hello":"world"}`)
	h1 := SHA256Hex(c)
	h2 := SHA256Hex(c)
	if h1 != h2 {
		t.Fatalf("hash unstable: %s vs %s", h1, h2)
	}
	if !strings.HasPrefix(h1, "0x") || len(h1) != 66 {
		t.Fatalf("malformed hash: %s", h1)
	}
}

func TestBytes_NilNumber(t *testing.T) {
	m := map[string]any{"a": nil, "b": true, "c": false, "d": []any{}}
	c, err := Bytes(m)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":null,"b":true,"c":false,"d":[]}`
	if string(c) != want {
		t.Fatalf("got %s, want %s", c, want)
	}
}
