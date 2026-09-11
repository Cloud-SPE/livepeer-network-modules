package config

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseOverlayYAML_Empty(t *testing.T) {
	o, err := ParseOverlayYAML(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := o.FindByAddress("0xabcdef0000000000000000000000000000000000"); ok {
		t.Fatal("empty overlay should have no entries")
	}
}

const sampleOverlay = `
overlay:
  - eth_address: "0xABCDef0000000000000000000000000000000000"
    enabled: true
    tier_allowed: [free, prepaid]
    weight: 50
    unsigned_allowed: false
    pin:
      - id: side-channel-1
        url: https://internal.example.com:8935
        weight: 10
        capabilities:
          - name: "openai:/v1/embeddings"
            work_unit: token
            offerings:
              - id: text-embedding-3-small
                price_per_work_unit_wei: "100"
        tier_allowed: [prepaid]
  - eth_address: "0xfedcba0000000000000000000000000000000000"
    enabled: false
`

func TestParseOverlayYAML_HappyPath(t *testing.T) {
	o, err := ParseOverlayYAML([]byte(sampleOverlay))
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(o.Entries))
	}
	e, ok := o.FindByAddress("0xabcdef0000000000000000000000000000000000")
	if !ok {
		t.Fatal("expected lookup to succeed (case-normalized)")
	}
	if !e.Enabled {
		t.Fatal("expected enabled=true")
	}
	if e.Weight != 50 {
		t.Fatalf("want weight 50, got %d", e.Weight)
	}
	if len(e.Pin) != 1 {
		t.Fatalf("want 1 pin, got %d", len(e.Pin))
	}
	pin := e.Pin[0]
	if pin.ID != "side-channel-1" || pin.URL != "https://internal.example.com:8935" {
		t.Fatalf("pin mismatch: %+v", pin)
	}
	if len(pin.Capabilities) != 1 {
		t.Fatalf("want 1 capability, got %d", len(pin.Capabilities))
	}

	disabled, _ := o.FindByAddress("0xfedcba0000000000000000000000000000000000")
	if disabled.Enabled {
		t.Fatal("expected enabled=false for second entry")
	}
}

func TestParseOverlayYAML_RejectsCases(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantSub string
	}{
		{
			name:    "duplicate-address",
			body:    "overlay:\n  - eth_address: \"0xabcdef0000000000000000000000000000000000\"\n  - eth_address: \"0xABCDEF0000000000000000000000000000000000\"\n",
			wantSub: "duplicate eth_address",
		},
		{
			name:    "weight-out-of-range",
			body:    "overlay:\n  - eth_address: \"0xabcdef0000000000000000000000000000000000\"\n    weight: 5000\n",
			wantSub: "weight: must be 1..1000",
		},
		{
			name:    "unknown-field",
			body:    "overlay:\n  - eth_address: \"0xabcdef0000000000000000000000000000000000\"\n    bogus: yes\n",
			wantSub: "field bogus not found",
		},
		{
			name:    "bad-address",
			body:    "overlay:\n  - eth_address: \"not-an-address\"\n",
			wantSub: "eth_address",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseOverlayYAML([]byte(c.body))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Fatalf("err %q does not contain %q", err.Error(), c.wantSub)
			}
		})
	}
}

func TestEmptyOverlay_NilSafe(t *testing.T) {
	var o *Overlay
	if _, ok := o.FindByAddress("0xabcdef0000000000000000000000000000000000"); ok {
		t.Fatal("nil overlay should report not-found, not panic")
	}
}

// `warm` was removed from the manifest schema in the v3.0.1 reset
// (exec-plan 0004), but the overlay's parser kept accepting it and threw
// it away. An operator who declared it got silence: valid config, no
// effect, no warning. Strict parsing now says so.
func TestParseOverlayYAML_RejectsRemovedWarmField(t *testing.T) {
	const y = `
overlay:
  - eth_address: "0xabc0000000000000000000000000000000000000"
    pin:
      - id: node-1
        url: "https://node1.example"
        capabilities:
          - name: "openai:/v1/embeddings"
            work_unit: token
            offerings:
              - id: text-embedding-3-small
                price_per_work_unit_wei: "100"
                warm: true
`
	if _, err := ParseOverlayYAML([]byte(y)); err == nil {
		t.Fatal("expected an error for the removed `warm` field; accepting and discarding it " +
			"is how an operator ends up believing a knob works")
	}
}

// The declared compatibility axes live in capability `extra`
// (offering-axes.md). A pin that drops it projects a route a consumer
// can see the price of and still cannot tell whether it can speak to.
func TestParseOverlayYAML_CarriesCapabilityExtra(t *testing.T) {
	const y = `
overlay:
  - eth_address: "0xabc0000000000000000000000000000000000000"
    pin:
      - id: node-1
        url: "https://node1.example"
        capabilities:
          - name: "openai:/v1/chat/completions"
            protocol: "paid-job/v1"
            work_unit: token
            extra:
              openai:
                model: gpt-oss-20b
              transports: [unary, stream]
            offerings:
              - id: default
                price_per_work_unit_wei: "100"
                per_units: 1000
`
	o, err := ParseOverlayYAML([]byte(y))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(o.Entries) != 1 || len(o.Entries[0].Pin) != 1 {
		t.Fatalf("unexpected overlay shape: %+v", o.Entries)
	}
	caps := o.Entries[0].Pin[0].Capabilities
	if len(caps) != 1 {
		t.Fatalf("want 1 capability, got %d", len(caps))
	}
	if len(caps[0].Extra) == 0 {
		t.Fatal("capability extra was dropped; the declared axes never reach a consumer")
	}
	var got map[string]any
	if err := json.Unmarshal(caps[0].Extra, &got); err != nil {
		t.Fatalf("extra is not valid JSON: %v", err)
	}
	if _, ok := got["openai"]; !ok {
		t.Fatalf("extra lost its contents: %s", caps[0].Extra)
	}
	if _, ok := got["transports"]; !ok {
		t.Fatalf("extra lost its contents: %s", caps[0].Extra)
	}
}
