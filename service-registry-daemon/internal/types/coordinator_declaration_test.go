package types

import (
	"encoding/json"
	"strings"
	"testing"
)

func envelopeWithExtra(t *testing.T, extra map[string]any) []byte {
	t.Helper()
	cap := map[string]any{
		"capability_id":      "livepeer:meet",
		"offering_id":        "default",
		"protocol":           "paid-session/v1",
		"session":            map[string]any{"descriptor_schema": "sfu-room/v1"},
		"work_unit":          map[string]any{"name": "participant_minutes"},
		"price_per_unit_wei": "10",
		"worker_url":         "https://broker.example.com",
	}
	if extra != nil {
		cap["extra"] = extra
	}
	env := map[string]any{
		"manifest": map[string]any{
			"spec_version":    "2.1.0",
			"orch":            map[string]any{"eth_address": "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"},
			"issued_at":       "2026-08-20T00:00:00Z",
			"expires_at":      "2030-01-01T00:00:00Z",
			"publication_seq": 1,
			"capabilities":    []any{cap},
		},
		"signature": map[string]any{"algorithm": "secp256k1", "value": "0x" + strings.Repeat("ab", 65)},
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestDeclarationKeysAreReservedInExtra: `extra` is opaque operator
// metadata and the signed declaration is authoritative. A tuple that
// puts a declaration key in extra is refused, rather than being silently
// corrected — an orch that could publish extra.protocol on a
// paid-session offering could make every consumer gating on protocol
// take the wrong open path.
func TestDeclarationKeysAreReservedInExtra(t *testing.T) {
	for _, key := range []string{"protocol", "job", "session"} {
		_, err := DecodeCoordinatorEnvelope(envelopeWithExtra(t, map[string]any{key: "anything"}))
		if err == nil {
			t.Fatalf("extra.%s was accepted; the signed declaration owns that key", key)
		}
		if !strings.Contains(err.Error(), key) {
			t.Fatalf("error for extra.%s does not name the key: %v", key, err)
		}
	}
}

// TestDeclarationReachesTheProjection: protocol arrives as a typed field
// on the capability (so it can be projected typed onto SelectedRoute),
// and the axes still ride in extra for consumers that read them.
func TestDeclarationReachesTheProjection(t *testing.T) {
	sm, err := DecodeCoordinatorEnvelope(envelopeWithExtra(t, map[string]any{"region": "us-east"}))
	if err != nil {
		t.Fatal(err)
	}
	m, err := sm.ToManifest()
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Nodes) != 1 || len(m.Nodes[0].Capabilities) != 1 {
		t.Fatalf("projection produced %d nodes", len(m.Nodes))
	}
	c := m.Nodes[0].Capabilities[0]
	if c.Protocol != "paid-session/v1" {
		t.Fatalf("capability protocol = %q; want the signed tuple's", c.Protocol)
	}
	var extra map[string]any
	if err := json.Unmarshal(c.Extra, &extra); err != nil {
		t.Fatal(err)
	}
	if extra["protocol"] != "paid-session/v1" {
		t.Fatalf("extra.protocol = %v; want the signed value mirrored", extra["protocol"])
	}
	if _, ok := extra["session"]; !ok {
		t.Fatal("declared axes did not reach extra; gateways select on them")
	}
	if extra["region"] != "us-east" {
		t.Fatal("operator metadata was dropped")
	}
}
