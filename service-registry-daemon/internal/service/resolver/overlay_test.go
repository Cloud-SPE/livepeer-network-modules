package resolver

import (
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

func TestApplyOverlay_NoEntry_ApplyDefaults(t *testing.T) {
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	in := []types.ResolvedNode{{ID: "n", URL: "https://x", Weight: 0}}
	out := applyOverlay(addr, in, config.EmptyOverlay())
	if len(out) != 1 {
		t.Fatalf("expected 1, got %d", len(out))
	}
	if out[0].Weight != 100 {
		t.Fatalf("expected default weight 100, got %d", out[0].Weight)
	}
}

const overlayWithPin = `
overlay:
  - eth_address: "0xabcdef0000000000000000000000000000000000"
    enabled: true
    tier_allowed: [free, prepaid]
    weight: 50
    pin:
      - id: pin-1
        url: "https://pinned.example.com"
        weight: 25
        capabilities:
          - name: "openai:/v1/chat/completions"
            offerings:
              - id: gpt-1
        tier_allowed: [prepaid]
`

func TestApplyOverlay_StampsPolicyAndAppendsPin(t *testing.T) {
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	o, err := config.ParseOverlayYAML([]byte(overlayWithPin))
	if err != nil {
		t.Fatal(err)
	}
	in := []types.ResolvedNode{{ID: "manifest-1", URL: "https://manifest.x", Weight: 100, Enabled: true}}
	out := applyOverlay(addr, in, o)
	if len(out) != 2 {
		t.Fatalf("expected 2 nodes (manifest + pin), got %d", len(out))
	}
	// Manifest node gets policy from overlay.
	mn := out[0]
	if mn.Weight != 50 {
		t.Fatalf("manifest weight = %d", mn.Weight)
	}
	if len(mn.TierAllowed) != 2 {
		t.Fatalf("manifest tier: %+v", mn.TierAllowed)
	}
	// Pin node uses its own weight + tier.
	pn := out[1]
	if pn.ID != "pin-1" || pn.Weight != 25 {
		t.Fatalf("pin: %+v", pn)
	}
	if len(pn.TierAllowed) != 1 || pn.TierAllowed[0] != "prepaid" {
		t.Fatalf("pin tier: %+v", pn.TierAllowed)
	}
}

func TestMergeTier(t *testing.T) {
	parent := []string{"free"}
	pin := []string{"prepaid"}
	if got := mergeTier(parent, pin); got[0] != "prepaid" {
		t.Fatalf("pin tier should win: %v", got)
	}
	if got := mergeTier(parent, nil); got[0] != "free" {
		t.Fatalf("parent fallback: %v", got)
	}
	if got := mergeTier(nil, nil); got != nil {
		t.Fatalf("nil/nil = %v", got)
	}
}

func TestChooseWeight(t *testing.T) {
	if got := chooseWeight(50, 25); got != 25 {
		t.Fatalf("pin weight should win: %d", got)
	}
	if got := chooseWeight(50, 0); got != 50 {
		t.Fatalf("parent fallback: %d", got)
	}
}

func TestSignaturePolicyAllows(t *testing.T) {
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	cases := []struct {
		name     string
		overlay  string
		allowReq bool
		status   types.SignatureStatus
		want     bool
	}{
		{"verified-always", "", false, types.SigVerified, true},
		{"legacy-always", "", false, types.SigLegacy, true},
		{"unsigned-rejected-default", "", false, types.SigUnsigned, false},
		{"unsigned-allowed-via-request", "", true, types.SigUnsigned, true},
		{"unsigned-allowed-via-overlay",
			"overlay:\n  - eth_address: \"0xabcdef0000000000000000000000000000000000\"\n    unsigned_allowed: true\n",
			false, types.SigUnsigned, true},
		{"invalid-never", "", true, types.SigInvalid, false},
		{"unknown-never", "", false, types.SigUnknown, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := config.EmptyOverlay()
			if c.overlay != "" {
				parsed, err := config.ParseOverlayYAML([]byte(c.overlay))
				if err != nil {
					t.Fatal(err)
				}
				o = parsed
			}
			if got := signaturePolicyAllows(addr, o, c.allowReq, c.status); got != c.want {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

func TestDetectMode_EdgeCases(t *testing.T) {
	cases := []struct {
		in   string
		want types.ResolveMode
	}{
		{"   ", types.ModeUnknown},
		{"http://localhost:8935", types.ModeWellKnown},
		{"   https://x   ", types.ModeWellKnown},
	}
	for _, c := range cases {
		if got := detectMode(c.in); got != c.want {
			t.Fatalf("detectMode(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestDecodeCSV_HappyPath(t *testing.T) {
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	// "https://x,1,base64-of-{...nodes...}"
	uri := "https://x,1,eyJ2ZXJzaW9uIjoxLCJub2RlcyI6W3sidXJsIjoiaHR0cHM6Ly9jc3YuZXhhbXBsZS5jb20iLCJsYXQiOjQwLjcxLCJsb24iOi03NH1dfQ=="
	defURL, nodes, err := decodeCSV(addr, uri)
	if err != nil {
		t.Fatal(err)
	}
	if defURL != "https://x" {
		t.Fatalf("defURL: %s", defURL)
	}
	if len(nodes) != 1 {
		t.Fatalf("nodes: %d", len(nodes))
	}
	if nodes[0].URL != "https://csv.example.com" {
		t.Fatalf("node URL: %s", nodes[0].URL)
	}
	if nodes[0].URL == "" {
		t.Fatalf("missing URL: %+v", nodes[0])
	}
}

func TestDecodeCSV_BadShape(t *testing.T) {
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	if _, _, err := decodeCSV(addr, "no-commas"); err == nil {
		t.Fatal("expected error")
	}
	if _, _, err := decodeCSV(addr, "https://x,1,!!notbase64!!"); err == nil {
		t.Fatal("expected error on bad base64")
	}
	if _, _, err := decodeCSV(addr, "https://x,1,eyJiYWQiOiJqc29uIn0_!"); err == nil {
		t.Fatal("expected error on bad final segment")
	}
}

func TestDecodeCSV_IPPort(t *testing.T) {
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	// payload: {"nodes":[{"ip":"10.0.0.1","port":8935}]}
	uri := "https://x,1,eyJub2RlcyI6W3siaXAiOiIxMC4wLjAuMSIsInBvcnQiOjg5MzV9XX0="
	_, nodes, err := decodeCSV(addr, uri)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].URL != "https://10.0.0.1:8935" {
		t.Fatalf("node: %+v", nodes)
	}
}

func TestNonZeroDuration(t *testing.T) {
	if got := nonZeroDuration(0, 5); got != 5 {
		t.Fatalf("zero fallback: %v", got)
	}
	if got := nonZeroDuration(-1, 5); got != 5 {
		t.Fatalf("negative fallback: %v", got)
	}
	if got := nonZeroDuration(7, 5); got != 7 {
		t.Fatalf("non-zero passthrough: %v", got)
	}
}

func TestHexNibble(t *testing.T) {
	cases := map[byte]struct {
		out byte
		ok  bool
	}{
		'0': {0, true},
		'9': {9, true},
		'a': {10, true},
		'f': {15, true},
		'A': {10, true},
		'F': {15, true},
		'g': {0, false},
		'/': {0, false},
	}
	for in, want := range cases {
		got, ok := hexNibble(in)
		if got != want.out || ok != want.ok {
			t.Fatalf("hexNibble(%c) = (%v,%v), want (%v,%v)", in, got, ok, want.out, want.ok)
		}
	}
}
