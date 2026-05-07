// Package sessioncontrolplusmedia is the gateway-side WebRTC media-plane
// adapter for the session-control-plus-media@v0 interaction mode. It
// proxies SDP offer/answer between the customer's browser and the
// broker's media plane, without transcoding or inspecting media bytes.
//
// The control-plane WebSocket lives in the TS half at
// gateway-adapters/ts/src/modes/session-control-plus-media.ts; this
// half handles only the WebRTC media surface.
package sessioncontrolplusmedia

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/pion/webrtc/v3"
)

// MediaDescriptor is the capability-shaped media-plane descriptor the
// broker returns in its session-open response. The adapter copies the
// `webrtc_signal_url` field into per-session state; other fields pass
// through unchanged.
type MediaDescriptor struct {
	// Schema is the capability-defined schema name (e.g.
	// `webrtc-pass-through@v0`).
	Schema string
	// WebRTCSignalURL is the broker's HTTPS endpoint that accepts the
	// SDP offer and returns the answer. Required when Schema marks a
	// WebRTC media plane.
	WebRTCSignalURL string
	// Auth is the bearer token (if any) the gateway forwards on the
	// signalling POST.
	Auth string
}

// Mediator owns per-session WebRTC SDP exchanges. It is constructed
// per-gateway and accepts customer signalling traffic via Negotiate.
type Mediator struct {
	api *webrtc.API

	mu       sync.Mutex
	sessions map[string]*peerSession
}

type peerSession struct {
	sessionID string
	media     MediaDescriptor

	mu        sync.Mutex
	pc        *webrtc.PeerConnection
	createdAt time.Time
}

// Config wires the gateway-side WebRTC media plane.
type Config struct {
	// SignalListenAddr is the TCP address the gateway binds the
	// WebRTC signalling endpoint. Default `:8443`.
	SignalListenAddr string

	// PortRangeMin / PortRangeMax bound the UDP port range the SFU
	// pass-through binds for ICE. The gateway operator MUST open
	// these in their firewall / cloud security group. Default
	// 40000-40099.
	PortRangeMin uint16
	PortRangeMax uint16
}

// NewMediator builds a Mediator with the given config. The signalling
// listener is wired in by the gateway operator; this constructor
// prepares the pion API surface and per-session map only.
func NewMediator(cfg Config) (*Mediator, error) {
	se := webrtc.SettingEngine{}
	if cfg.PortRangeMin > 0 && cfg.PortRangeMax >= cfg.PortRangeMin {
		if err := se.SetEphemeralUDPPortRange(cfg.PortRangeMin, cfg.PortRangeMax); err != nil {
			return nil, fmt.Errorf("set udp port range: %w", err)
		}
	}
	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))
	return &Mediator{
		api:      api,
		sessions: map[string]*peerSession{},
	}, nil
}

// Open registers a new session. Subsequent Negotiate calls keyed by
// sessionID find this state and run the SDP exchange.
func (m *Mediator) Open(sessionID string, media MediaDescriptor) error {
	if sessionID == "" {
		return errors.New("session: empty sessionID")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sessions[sessionID]; exists {
		return errors.New("session: already open")
	}
	m.sessions[sessionID] = &peerSession{
		sessionID: sessionID,
		media:     media,
		createdAt: time.Now(),
	}
	return nil
}

// Close terminates a session, closing the underlying PeerConnection
// (if any). Idempotent.
func (m *Mediator) Close(sessionID string) {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	delete(m.sessions, sessionID)
	m.mu.Unlock()
	if !ok {
		return
	}
	sess.mu.Lock()
	pc := sess.pc
	sess.pc = nil
	sess.mu.Unlock()
	if pc != nil {
		_ = pc.Close()
	}
}

// Active returns the count of open sessions.
func (m *Mediator) Active() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

// Negotiate runs the SDP exchange for a session. The customer's
// browser POSTs its SDP offer to the gateway's signalling endpoint,
// which calls Negotiate; the answer (returned here as a session
// description) is sent back as the HTTP response.
//
// In SFU pass-through mode the gateway does NOT terminate the media
// plane — the answer the customer receives is the broker's, with ICE
// candidates pointing at the broker's media ports. The gateway's
// PeerConnection (when one is created) is used only for resource
// accounting; media bytes do not transit the gateway process.
func (m *Mediator) Negotiate(ctx context.Context, sessionID string, customerOffer webrtc.SessionDescription) (webrtc.SessionDescription, error) {
	m.mu.Lock()
	sess, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return webrtc.SessionDescription{}, errors.New("session: not found or closed")
	}
	if sess.media.WebRTCSignalURL == "" {
		return webrtc.SessionDescription{}, errors.New("session: no webrtc_signal_url in media descriptor")
	}

	answer, err := proxyToBroker(ctx, sess.media, customerOffer)
	if err != nil {
		return webrtc.SessionDescription{}, err
	}

	// Build a placeholder PeerConnection for resource accounting.
	// The pass-through model means we don't actually establish a
	// peer; the customer connects directly to the broker via the
	// candidates in the broker's answer SDP.
	sess.mu.Lock()
	if sess.pc != nil {
		_ = sess.pc.Close()
	}
	pc, err := m.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		sess.mu.Unlock()
		return webrtc.SessionDescription{}, fmt.Errorf("new PeerConnection: %w", err)
	}
	sess.pc = pc
	sess.mu.Unlock()

	return answer, nil
}
