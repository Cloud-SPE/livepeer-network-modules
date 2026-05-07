// Package webrtc owns the per-session pion PeerConnection wrapper +
// SDP/ICE plumbing used by the session-control-plus-media driver.
//
// One PeerConnection per session. Tracks demux: incoming customer
// tracks forward as raw RTP to the runner. Tracks mux: runner-emitted
// RTP routes back through egress. The relay stays codec-opaque per
// plan 0012-followup §6.4 — workloads that prefer decoded frames
// decode in the runner.
package webrtc

import (
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/pion/webrtc/v3"
)

// Config governs the per-broker pion settings.
type Config struct {
	// PublicIP is the host IP advertised in ICE candidates. Empty
	// defers to pion's default (host-outbound interface auto-detected
	// at PeerConnection setup time).
	PublicIP string

	// UDPPortMin / UDPPortMax bound the UDP range pion binds for
	// media. Operator firewall must open this range.
	UDPPortMin uint16
	UDPPortMax uint16
}

// DefaultConfig returns the recommended defaults from §10.1.
func DefaultConfig() Config {
	return Config{
		UDPPortMin: 40000,
		UDPPortMax: 49999,
	}
}

// Validate checks the config for obvious errors.
func (c Config) Validate() error {
	if c.UDPPortMin == 0 {
		return errors.New("webrtc: udp-port-min must be > 0")
	}
	if c.UDPPortMax < c.UDPPortMin {
		return fmt.Errorf("webrtc: udp-port-max (%d) < udp-port-min (%d)", c.UDPPortMax, c.UDPPortMin)
	}
	if c.PublicIP != "" && net.ParseIP(c.PublicIP) == nil {
		return fmt.Errorf("webrtc: public-ip %q is not a valid IP", c.PublicIP)
	}
	return nil
}

// Engine wraps a pion settings + media engine pair shared across all
// per-session relays. Constructed once at server startup.
type Engine struct {
	api  *webrtc.API
	cfg  Config
}

// NewEngine builds the shared pion API with the configured port range
// + public IP. v0.1 supports only the default codec set (Opus audio +
// VP8/VP9/H264 video) as registered by pion's default media engine.
func NewEngine(cfg Config) (*Engine, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	settings := webrtc.SettingEngine{}
	if err := settings.SetEphemeralUDPPortRange(cfg.UDPPortMin, cfg.UDPPortMax); err != nil {
		return nil, fmt.Errorf("webrtc: udp port range: %w", err)
	}
	if cfg.PublicIP != "" {
		settings.SetNAT1To1IPs([]string{cfg.PublicIP}, webrtc.ICECandidateTypeHost)
	}
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		return nil, fmt.Errorf("webrtc: register codecs: %w", err)
	}
	api := webrtc.NewAPI(
		webrtc.WithSettingEngine(settings),
		webrtc.WithMediaEngine(mediaEngine),
	)
	return &Engine{api: api, cfg: cfg}, nil
}

// Config returns the engine's configured pion settings.
func (e *Engine) Config() Config { return e.cfg }

// Relay is a per-session wrapper around a pion PeerConnection. The
// driver stands one up at media.negotiate.start and tears it down at
// session teardown.
type Relay struct {
	mu sync.Mutex

	pc *webrtc.PeerConnection

	// onIngress fires once per inbound track from the customer.
	onIngress func(*webrtc.TrackRemote)

	// onICEState fires on every connection-state transition. Used by
	// the driver to emit media.ready / media.failed envelopes.
	onICEState func(webrtc.PeerConnectionState)
}

// NewRelay creates a per-session PeerConnection. The configuration
// passes a single SDP offer/answer pair end-to-end; ICE candidates
// trickle over the control-WS per Q5 lock.
func (e *Engine) NewRelay() (*Relay, error) {
	pc, err := e.api.NewPeerConnection(webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{},
	})
	if err != nil {
		return nil, fmt.Errorf("webrtc: new peer connection: %w", err)
	}
	r := &Relay{pc: pc}
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		r.mu.Lock()
		cb := r.onIngress
		r.mu.Unlock()
		if cb != nil {
			cb(track)
		}
	})
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		r.mu.Lock()
		cb := r.onICEState
		r.mu.Unlock()
		if cb != nil {
			cb(state)
		}
	})
	return r, nil
}

// SetIngressHandler installs the callback that fires for each inbound
// customer track. Setting nil clears the callback.
func (r *Relay) SetIngressHandler(cb func(*webrtc.TrackRemote)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onIngress = cb
}

// SetICEStateHandler installs the connection-state-change callback.
func (r *Relay) SetICEStateHandler(cb func(webrtc.PeerConnectionState)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onICEState = cb
}

// PeerConnection exposes the underlying pion PC for SDP/ICE plumbing
// in sdp.go.
func (r *Relay) PeerConnection() *webrtc.PeerConnection {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.pc
}

// AddEgressTrack registers a server-emitted track. Called once the
// runner side reports it has frames to emit. Returns the wrapped
// pion sender so the caller can write RTP payloads via WriteRTP.
func (r *Relay) AddEgressTrack(track webrtc.TrackLocal) (*webrtc.RTPSender, error) {
	r.mu.Lock()
	pc := r.pc
	r.mu.Unlock()
	if pc == nil {
		return nil, errors.New("webrtc: relay torn down")
	}
	return pc.AddTrack(track)
}

// Close tears down the underlying PeerConnection. Idempotent.
func (r *Relay) Close() error {
	r.mu.Lock()
	pc := r.pc
	r.pc = nil
	r.mu.Unlock()
	if pc == nil {
		return nil
	}
	return pc.Close()
}
