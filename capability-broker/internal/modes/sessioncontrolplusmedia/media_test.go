package sessioncontrolplusmedia

import (
	"testing"

	pwebrtc "github.com/pion/webrtc/v3"

	mediawebrtc "github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/media/webrtc"
)

func newTestEngine(t *testing.T) *mediawebrtc.Engine {
	t.Helper()
	cfg := mediawebrtc.DefaultConfig()
	cfg.UDPPortMin = 51000 + uint16(t.Name()[0]%10)*100
	cfg.UDPPortMax = cfg.UDPPortMin + 99
	e, err := mediawebrtc.NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e
}

func TestMediaRelayHandleControlEnvelopeUnknownType(t *testing.T) {
	t.Parallel()
	engine := newTestEngine(t)
	r, err := engine.NewRelay()
	if err != nil {
		t.Fatalf("NewRelay: %v", err)
	}
	defer r.Close()
	mr := NewMediaRelay("sess_x", r, nil)

	_, hasReply, err := mr.HandleControlEnvelope(ControlEnvelope{Type: "media.unknown"})
	if err != nil {
		t.Fatalf("unknown media envelope: %v", err)
	}
	if hasReply {
		t.Fatal("unknown envelope should not produce a reply")
	}
}

func TestMediaRelayHandleSDPOfferEmitsAnswer(t *testing.T) {
	t.Parallel()
	engine := newTestEngine(t)
	r, err := engine.NewRelay()
	if err != nil {
		t.Fatalf("NewRelay: %v", err)
	}
	defer r.Close()

	clientEngine := newTestEngine(t)
	cli, err := clientEngine.NewRelay()
	if err != nil {
		t.Fatalf("client NewRelay: %v", err)
	}
	defer cli.Close()

	cli.PeerConnection().AddTransceiverFromKind(pwebrtc.RTPCodecTypeAudio)
	offer, err := cli.PeerConnection().CreateOffer(nil)
	if err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	if err := cli.PeerConnection().SetLocalDescription(offer); err != nil {
		t.Fatalf("SetLocalDescription: %v", err)
	}

	mr := NewMediaRelay("sess_x", r, nil)
	body, err := mediawebrtc.EncodeSDP(offer)
	if err != nil {
		t.Fatalf("EncodeSDP: %v", err)
	}
	reply, hasReply, err := mr.HandleControlEnvelope(ControlEnvelope{
		Type: TypeMediaSDPOffer,
		Body: body,
	})
	if err != nil {
		t.Fatalf("HandleControlEnvelope: %v", err)
	}
	if !hasReply {
		t.Fatal("expected media.sdp.answer reply")
	}
	if reply.Type != TypeMediaSDPAnswer {
		t.Fatalf("reply type: got %q want %q", reply.Type, TypeMediaSDPAnswer)
	}
	answer, err := mediawebrtc.DecodeSDP(reply.Body)
	if err != nil {
		t.Fatalf("DecodeSDP(answer): %v", err)
	}
	if answer.Type != pwebrtc.SDPTypeAnswer {
		t.Fatalf("answer SDPType: got %v want answer", answer.Type)
	}
}

func TestMediaRelayHandleICECandidateBeforeOfferErrors(t *testing.T) {
	t.Parallel()
	engine := newTestEngine(t)
	r, err := engine.NewRelay()
	if err != nil {
		t.Fatalf("NewRelay: %v", err)
	}
	defer r.Close()
	mr := NewMediaRelay("sess_x", r, nil)
	body, _ := mediawebrtc.EncodeICECandidate(pwebrtc.ICECandidateInit{Candidate: "candidate:1 1 udp 1 1.2.3.4 1 typ host"})
	_, _, err = mr.HandleControlEnvelope(ControlEnvelope{Type: TypeMediaICECandidate, Body: body})
	if err == nil {
		t.Fatal("expected error adding ICE candidate before remote description")
	}
}
