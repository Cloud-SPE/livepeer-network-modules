package sessioncontrolplusmedia

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pion/webrtc/v3"
)

func TestMediator_OpenCloseActive(t *testing.T) {
	m, err := NewMediator(Config{PortRangeMin: 40000, PortRangeMax: 40009})
	if err != nil {
		t.Fatalf("NewMediator: %v", err)
	}

	if got := m.Active(); got != 0 {
		t.Fatalf("Active=%d want 0", got)
	}

	if err := m.Open("sess-a", MediaDescriptor{Schema: "webrtc-pass-through@v0", WebRTCSignalURL: "https://broker/x"}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got := m.Active(); got != 1 {
		t.Errorf("Active after Open=%d want 1", got)
	}

	if err := m.Open("sess-a", MediaDescriptor{}); err == nil {
		t.Errorf("duplicate Open returned nil error; want failure")
	}

	m.Close("sess-a")
	if got := m.Active(); got != 0 {
		t.Errorf("Active after Close=%d want 0", got)
	}

	// Close on missing session is a no-op.
	m.Close("nope")
}

func TestMediator_Negotiate_ProxiesOfferAnswer(t *testing.T) {
	var receivedOffer offerEnvelope
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &receivedOffer)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(answerEnvelope{
			Type: "answer",
			SDP:  "v=0\r\no=- 1 1 IN IP4 0.0.0.0\r\ns=-\r\nt=0 0\r\n",
		})
	}))
	defer broker.Close()

	m, err := NewMediator(Config{})
	if err != nil {
		t.Fatalf("NewMediator: %v", err)
	}
	defer m.Close("sess-x")

	media := MediaDescriptor{
		Schema:          "webrtc-pass-through@v0",
		WebRTCSignalURL: broker.URL,
		Auth:            "secret-bearer",
	}
	if err := m.Open("sess-x", media); err != nil {
		t.Fatalf("Open: %v", err)
	}

	offer := webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  "v=0\r\no=- 0 0 IN IP4 127.0.0.1\r\ns=-\r\nt=0 0\r\n",
	}
	answer, err := m.Negotiate(context.Background(), "sess-x", offer)
	if err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if answer.Type != webrtc.SDPTypeAnswer {
		t.Errorf("answer.Type=%v want answer", answer.Type)
	}
	if answer.SDP == "" {
		t.Error("empty answer SDP")
	}
	if receivedOffer.Type != "offer" {
		t.Errorf("broker received offer.Type=%q want offer", receivedOffer.Type)
	}
	if receivedOffer.SDP != offer.SDP {
		t.Errorf("broker received SDP differs from customer offer")
	}
}

func TestMediator_Negotiate_NotFound(t *testing.T) {
	m, _ := NewMediator(Config{})
	_, err := m.Negotiate(context.Background(), "missing", webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: "x"})
	if err == nil {
		t.Fatal("Negotiate on missing session returned nil error")
	}
}

func TestMediator_Negotiate_BrokerError(t *testing.T) {
	broker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusServiceUnavailable)
	}))
	defer broker.Close()

	m, _ := NewMediator(Config{})
	if err := m.Open("sess-err", MediaDescriptor{
		Schema:          "webrtc-pass-through@v0",
		WebRTCSignalURL: broker.URL,
	}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	_, err := m.Negotiate(context.Background(), "sess-err", webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: "x"})
	if err == nil {
		t.Fatal("Negotiate with 503 broker returned nil error")
	}
}
