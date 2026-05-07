package sessioncontrolplusmedia

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"

	pwebrtc "github.com/pion/webrtc/v3"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/media/sessionrunner"
	mediawebrtc "github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/media/webrtc"
	srpb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/sessionrunner/v1"
)

// MediaRelay is the per-session glue between the pion PeerConnection
// and the runner's gRPC media-frame stream. The driver constructs one
// per session at session-open, attaches it to the runner-backend, and
// drives SDP/ICE plumbing via media.* envelopes on the control-WS.
type MediaRelay struct {
	sessionID string
	relay     *mediawebrtc.Relay
	stream    *sessionrunner.MediaRelay

	mu    sync.Mutex
	tracksByID map[string]*pwebrtc.TrackLocalStaticRTP
}

// NewMediaRelay returns a relay paired with the supplied pion + IPC
// handles.
func NewMediaRelay(sessionID string, relay *mediawebrtc.Relay, stream *sessionrunner.MediaRelay) *MediaRelay {
	return &MediaRelay{
		sessionID:  sessionID,
		relay:      relay,
		stream:     stream,
		tracksByID: make(map[string]*pwebrtc.TrackLocalStaticRTP),
	}
}

// Run starts the goroutines that pump RTP frames in both directions.
// Returns when ctx is canceled or either underlying handle errors.
func (m *MediaRelay) Run(ctx context.Context) {
	if m == nil || m.relay == nil || m.stream == nil {
		return
	}
	m.relay.SetIngressHandler(func(track *pwebrtc.TrackRemote) {
		go m.pumpIngress(ctx, track)
	})
	go m.pumpEgress(ctx)
	<-ctx.Done()
}

// pumpIngress copies customer-published RTP packets onto the runner
// gRPC stream tagged DIRECTION_INGRESS.
func (m *MediaRelay) pumpIngress(ctx context.Context, track *pwebrtc.TrackRemote) {
	buf := make([]byte, 1500)
	for {
		if ctx.Err() != nil {
			return
		}
		n, _, err := track.Read(buf)
		if err != nil {
			return
		}
		frame := &srpb.MediaFrame{
			Direction:   srpb.Direction_DIRECTION_INGRESS,
			TrackId:     track.ID(),
			PayloadType: uint32(track.PayloadType()),
			Rtp:         append([]byte(nil), buf[:n]...),
		}
		if err := m.stream.Send(frame); err != nil {
			log.Printf("session-control-plus-media: session=%s ingress send: %v", m.sessionID, err)
			return
		}
	}
}

// pumpEgress reads runner-emitted RTP frames off the gRPC stream and
// writes them onto the corresponding egress TrackLocal. Tracks are
// minted lazily: the first frame seen for a track id allocates the
// pion TrackLocal and adds it to the PeerConnection.
func (m *MediaRelay) pumpEgress(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		frame, err := m.stream.Recv()
		if err != nil {
			return
		}
		if frame.GetDirection() != srpb.Direction_DIRECTION_EGRESS {
			continue
		}
		track, err := m.ensureEgressTrack(frame.GetTrackId(), frame.GetPayloadType())
		if err != nil {
			log.Printf("session-control-plus-media: session=%s ensure egress track: %v", m.sessionID, err)
			continue
		}
		if _, err := track.Write(frame.GetRtp()); err != nil {
			log.Printf("session-control-plus-media: session=%s egress write: %v", m.sessionID, err)
		}
	}
}

func (m *MediaRelay) ensureEgressTrack(trackID string, payloadType uint32) (*pwebrtc.TrackLocalStaticRTP, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tracksByID[trackID]; ok {
		return t, nil
	}
	codec := pwebrtc.RTPCodecCapability{MimeType: pwebrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}
	if payloadType >= 96 && payloadType <= 99 {
		codec = pwebrtc.RTPCodecCapability{MimeType: pwebrtc.MimeTypeVP8, ClockRate: 90000}
	}
	track, err := pwebrtc.NewTrackLocalStaticRTP(codec, trackID, "session-runner")
	if err != nil {
		return nil, err
	}
	if _, err := m.relay.AddEgressTrack(track); err != nil {
		return nil, err
	}
	m.tracksByID[trackID] = track
	return track, nil
}

// HandleControlEnvelope inspects a control-WS envelope of type
// media.* and applies the corresponding pion action, returning any
// envelope the broker should emit back to the customer (e.g.
// media.sdp.answer in response to media.sdp.offer).
func (m *MediaRelay) HandleControlEnvelope(env ControlEnvelope) (ControlEnvelope, bool, error) {
	if m == nil || m.relay == nil {
		return ControlEnvelope{}, false, errors.New("media-relay: not configured")
	}
	switch env.Type {
	case TypeMediaSDPOffer:
		offer, err := mediawebrtc.DecodeSDP(env.Body)
		if err != nil {
			return ControlEnvelope{}, false, err
		}
		answer, err := m.relay.HandleClientOffer(offer)
		if err != nil {
			return ControlEnvelope{}, false, err
		}
		body, err := mediawebrtc.EncodeSDP(answer)
		if err != nil {
			return ControlEnvelope{}, false, err
		}
		return ControlEnvelope{Type: TypeMediaSDPAnswer, Body: body}, true, nil
	case TypeMediaICECandidate:
		c, err := mediawebrtc.DecodeICECandidate(env.Body)
		if err != nil {
			return ControlEnvelope{}, false, err
		}
		return ControlEnvelope{}, false, m.relay.AddRemoteICECandidate(c)
	}
	return ControlEnvelope{}, false, nil
}

// emitLocalCandidates pumps locally-gathered ICE candidates onto the
// outbound channel as media.ice.candidate envelopes.
func (m *MediaRelay) emitLocalCandidates(ctx context.Context, out chan<- ControlEnvelope) {
	if m == nil || m.relay == nil {
		return
	}
	for c := range m.relay.LocalCandidatesChannel() {
		body, err := json.Marshal(c)
		if err != nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case out <- ControlEnvelope{Type: TypeMediaICECandidate, Body: body}:
		}
	}
}
