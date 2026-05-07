package webrtc

import (
	"sync/atomic"
	"testing"

	pwebrtc "github.com/pion/webrtc/v3"
)

func TestConfigValidate(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"defaults", DefaultConfig(), false},
		{"zero_min", Config{UDPPortMin: 0, UDPPortMax: 1000}, true},
		{"max_lt_min", Config{UDPPortMin: 5000, UDPPortMax: 4000}, true},
		{"bad_ip", Config{UDPPortMin: 1, UDPPortMax: 2, PublicIP: "not-an-ip"}, true},
		{"good_ip", Config{UDPPortMin: 1, UDPPortMax: 2, PublicIP: "10.0.0.1"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate(%+v): got err=%v, wantErr=%v", tc.cfg, err, tc.wantErr)
			}
		})
	}
}

func TestNewEngineDefault(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.UDPPortMin = 50000
	cfg.UDPPortMax = 50100
	e, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	if e.Config().UDPPortMin != 50000 {
		t.Fatalf("Config.UDPPortMin: got %d, want 50000", e.Config().UDPPortMin)
	}
}

func TestRelayLifecycle(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.UDPPortMin = 50200
	cfg.UDPPortMax = 50300
	e, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	r, err := e.NewRelay()
	if err != nil {
		t.Fatalf("NewRelay: %v", err)
	}
	if r.PeerConnection() == nil {
		t.Fatal("PeerConnection nil after construction")
	}

	var gotIngress, gotState atomic.Bool
	r.SetIngressHandler(func(*pwebrtc.TrackRemote) { gotIngress.Store(true) })
	r.SetICEStateHandler(func(pwebrtc.PeerConnectionState) { gotState.Store(true) })

	if err := r.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("Close idempotent: %v", err)
	}
	_ = gotIngress.Load()
	_ = gotState.Load()
}

func TestSDPEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()
	original := pwebrtc.SessionDescription{
		Type: pwebrtc.SDPTypeOffer,
		SDP:  "v=0\r\no=- 0 0 IN IP4 0.0.0.0\r\n",
	}
	encoded, err := EncodeSDP(original)
	if err != nil {
		t.Fatalf("EncodeSDP: %v", err)
	}
	decoded, err := DecodeSDP(encoded)
	if err != nil {
		t.Fatalf("DecodeSDP: %v", err)
	}
	if decoded.Type != original.Type || decoded.SDP != original.SDP {
		t.Fatalf("round-trip mismatch: got %+v want %+v", decoded, original)
	}
}

func TestICECandidateEncodeDecode(t *testing.T) {
	t.Parallel()
	original := pwebrtc.ICECandidateInit{Candidate: "candidate:1 1 UDP 2122252543 10.0.0.1 50000 typ host"}
	encoded, err := EncodeICECandidate(original)
	if err != nil {
		t.Fatalf("EncodeICECandidate: %v", err)
	}
	decoded, err := DecodeICECandidate(encoded)
	if err != nil {
		t.Fatalf("DecodeICECandidate: %v", err)
	}
	if decoded.Candidate != original.Candidate {
		t.Fatalf("round-trip mismatch: got %+v want %+v", decoded, original)
	}
}

func TestRelayHandleOfferAfterClose(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	cfg.UDPPortMin = 50400
	cfg.UDPPortMax = 50500
	e, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	r, err := e.NewRelay()
	if err != nil {
		t.Fatalf("NewRelay: %v", err)
	}
	_ = r.Close()
	if _, err := r.HandleClientOffer(pwebrtc.SessionDescription{Type: pwebrtc.SDPTypeOffer}); err == nil {
		t.Fatal("expected error on torn-down relay")
	}
	if err := r.AddRemoteICECandidate(pwebrtc.ICECandidateInit{}); err == nil {
		t.Fatal("expected error on torn-down relay")
	}
}
