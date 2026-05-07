package webrtc

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/pion/webrtc/v3"
)

// HandleClientOffer applies a customer-supplied SDP offer to the
// relay's PeerConnection, generates an answer, and returns it. SDP
// ordering per §5.4 is client-offers — the broker sends
// media.negotiate.start; customer responds with media.sdp.offer; the
// answer here is what the broker emits as media.sdp.answer.
func (r *Relay) HandleClientOffer(offer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	r.mu.Lock()
	pc := r.pc
	r.mu.Unlock()
	if pc == nil {
		return webrtc.SessionDescription{}, errors.New("webrtc: relay torn down")
	}
	if offer.Type != webrtc.SDPTypeOffer {
		return webrtc.SessionDescription{}, fmt.Errorf("webrtc: expected offer, got %s", offer.Type)
	}
	if err := pc.SetRemoteDescription(offer); err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("webrtc: set remote description: %w", err)
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("webrtc: create answer: %w", err)
	}
	if err := pc.SetLocalDescription(answer); err != nil {
		return webrtc.SessionDescription{}, fmt.Errorf("webrtc: set local description: %w", err)
	}
	return answer, nil
}

// AddRemoteICECandidate accepts a trickle ICE candidate from the
// customer side. Empty Candidate string is the end-of-candidates
// marker per RFC 8838.
func (r *Relay) AddRemoteICECandidate(c webrtc.ICECandidateInit) error {
	r.mu.Lock()
	pc := r.pc
	r.mu.Unlock()
	if pc == nil {
		return errors.New("webrtc: relay torn down")
	}
	return pc.AddICECandidate(c)
}

// LocalCandidatesChannel returns a channel of locally-gathered ICE
// candidates the driver forwards to the customer over the control-WS
// as media.ice.candidate envelopes. Closes when ICE gathering ends.
func (r *Relay) LocalCandidatesChannel() <-chan webrtc.ICECandidateInit {
	out := make(chan webrtc.ICECandidateInit, 16)
	r.mu.Lock()
	pc := r.pc
	r.mu.Unlock()
	if pc == nil {
		close(out)
		return out
	}
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			close(out)
			return
		}
		select {
		case out <- c.ToJSON():
		default:
		}
	})
	return out
}

// EncodeSDP encodes a SessionDescription as the on-wire JSON shape
// envelopes carry on the control-WS.
func EncodeSDP(sdp webrtc.SessionDescription) ([]byte, error) {
	return json.Marshal(sdp)
}

// DecodeSDP parses a JSON SDP off the control-WS.
func DecodeSDP(data []byte) (webrtc.SessionDescription, error) {
	var sdp webrtc.SessionDescription
	if err := json.Unmarshal(data, &sdp); err != nil {
		return webrtc.SessionDescription{}, err
	}
	return sdp, nil
}

// EncodeICECandidate encodes a candidate for transport on the control-WS.
func EncodeICECandidate(c webrtc.ICECandidateInit) ([]byte, error) {
	return json.Marshal(c)
}

// DecodeICECandidate parses a candidate off the control-WS.
func DecodeICECandidate(data []byte) (webrtc.ICECandidateInit, error) {
	var c webrtc.ICECandidateInit
	if err := json.Unmarshal(data, &c); err != nil {
		return webrtc.ICECandidateInit{}, err
	}
	return c, nil
}
