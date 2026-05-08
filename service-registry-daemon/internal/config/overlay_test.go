package config

import (
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
                warm: true
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
