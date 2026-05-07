package sessioncontrolplusmedia

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/pion/webrtc/v3"
)

// proxyToBroker forwards the customer's SDP offer to the broker's
// WebRTC signalling endpoint and returns the broker's answer. The
// broker is the side that actually negotiates ICE; the gateway is a
// thin SDP proxy by design.
func proxyToBroker(ctx context.Context, media MediaDescriptor, offer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	body, err := json.Marshal(offerEnvelope{
		Type: offer.Type.String(),
		SDP:  offer.SDP,
	})
	if err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("marshal offer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, media.WebRTCSignalURL, bytes.NewReader(body))
	if err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("build broker signalling request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if media.Auth != "" {
		req.Header.Set("Authorization", "Bearer "+media.Auth)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("call broker signalling: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 400 {
		return webrtc.SessionDescription{}, fmt.Errorf("broker signalling status=%d body=%s",
			resp.StatusCode, string(respBody))
	}

	var env answerEnvelope
	if err := json.Unmarshal(respBody, &env); err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("decode broker answer: %w", err)
	}

	answer := webrtc.SessionDescription{SDP: env.SDP}
	switch env.Type {
	case "answer":
		answer.Type = webrtc.SDPTypeAnswer
	case "pranswer":
		answer.Type = webrtc.SDPTypePranswer
	default:
		return webrtc.SessionDescription{}, fmt.Errorf("unsupported broker answer type %q", env.Type)
	}
	return answer, nil
}

type offerEnvelope struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

type answerEnvelope struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}
