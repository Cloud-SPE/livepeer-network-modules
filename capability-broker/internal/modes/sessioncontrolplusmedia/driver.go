// Package sessioncontrolplusmedia implements the
// session-control-plus-media@v0 interaction-mode driver per
// livepeer-network-protocol/modes/session-control-plus-media.md.
//
// This package owns three planes:
//
//   - control-WS lifecycle (this file + controlws*.go): session-open POST,
//     WebSocket upgrade at /v1/cap/{session_id}/control, frame envelope,
//     heartbeat, reconnect-window state machine.
//   - media-plane provisioning: pion/webrtc relay (internal/media/webrtc/)
//     with SDP offer/answer + ICE trickle over the control-WS.
//   - session-runner subprocess (internal/media/sessionrunner/): per-session
//     workload-specific container; gRPC unix-socket IPC.
package sessioncontrolplusmedia

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes"
)

// Mode is the canonical mode-name@vN string for this driver.
const Mode = "session-control-plus-media@v0"

// DefaultExpiresIn is the no-attach deadline window. Spec
// recommendation: ~1 hour.
const DefaultExpiresIn = 1 * time.Hour

// Driver implements modes.Driver and owns the control-WS upgrade
// handler.
type Driver struct {
	store    *Store
	cfg      ControlWSConfig
	upgrader websocket.Upgrader
	backend  Backend
}

// Compile-time interface check.
var _ modes.Driver = (*Driver)(nil)

// New returns a driver bound to a session store + control-WS config.
// The store is shared with the broker's mux (the upgrade handler) and
// any watchdog goroutines.
func New(store *Store, cfg ControlWSConfig) *Driver {
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = 10 * time.Second
	}
	return &Driver{
		store: store,
		cfg:   cfg,
		upgrader: websocket.Upgrader{
			HandshakeTimeout: cfg.HandshakeTimeout,
			CheckOrigin:      func(r *http.Request) bool { return true },
		},
	}
}

// SetBackend wires the runner / media backend the control-WS relays
// against. Optional: when nil, workload envelopes are dropped silently
// (loopback mode used by the C1 standalone tests).
func (d *Driver) SetBackend(b Backend) { d.backend = b }

// Store returns the driver's session store. Exposed for the
// composition root (the mux registers a handler that reads it; the
// reconnect-window watchdog goroutine consumes it).
func (d *Driver) Store() *Store { return d.store }

// Config returns the driver's control-WS config (test helper).
func (d *Driver) Config() ControlWSConfig { return d.cfg }

// RunReconnectWatchdog starts the per-store reconnect-window expiry
// loop. Returns when ctx is canceled. Callers run it on its own
// goroutine.
func (d *Driver) RunReconnectWatchdog(ctx context.Context) {
	d.store.reconnectWatchdog(ctx, d, d.cfg.ReconnectWindow)
}

// Mode returns the mode identifier.
func (d *Driver) Mode() string { return Mode }

// Serve responds to the session-open POST with the required body
// fields and registers the session in the store.
func (d *Driver) Serve(_ context.Context, p modes.Params) error {
	if p.Request.Method != http.MethodPost {
		livepeerheader.WriteError(p.Writer, http.StatusMethodNotAllowed, livepeerheader.ErrModeUnsupported,
			"session-control-plus-media@v0 session-open is POST")
		return nil
	}

	sessID, err := generateSessionID()
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"session id: "+err.Error())
		return nil
	}

	base := p.Capability.Backend.URL
	ctrlURL, err := deriveControlURL(base, sessID)
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"control_url: "+err.Error())
		return nil
	}
	pubURL, err := derivePublishURL(base, sessID)
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"media.publish_url: "+err.Error())
		return nil
	}

	now := time.Now().UTC()
	rec := &SessionRecord{
		SessionID:    sessID,
		CapabilityID: p.Capability.ID,
		OfferingID:   p.Capability.OfferingID,
		OpenedAt:     now,
		ExpiresAt:    now.Add(DefaultExpiresIn),
		LiveCounter:  p.LiveCounter,
	}
	if err := d.store.Add(rec); err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"session store: "+err.Error())
		return nil
	}

	if d.backend != nil {
		ctx, cancel := context.WithCancel(context.Background())
		ctrl, err := d.backend.AttachControl(ctx, sessID)
		if err != nil {
			cancel()
			d.store.Remove(sessID)
			livepeerheader.WriteError(p.Writer, http.StatusInternalServerError, livepeerheader.ErrInternalError,
				"backend attach: "+err.Error())
			return nil
		}
		rec.control = &ctrl
		rec.Cancel = cancel
	}

	body := sessionOpenResponse{
		SessionID:  sessID,
		ControlURL: ctrlURL,
		Media: mediaDescriptor{
			PublishURL:  pubURL,
			PublishAuth: "webrtc:negotiate-on-control-ws",
		},
		ExpiresAt: rec.ExpiresAt.Format(time.RFC3339),
	}
	encoded, _ := json.Marshal(body)

	p.Writer.Header().Set("Content-Type", "application/json")
	p.Writer.Header().Set(livepeerheader.WorkUnits, "0")
	p.Writer.WriteHeader(http.StatusAccepted)
	_, _ = p.Writer.Write(encoded)
	return nil
}

type sessionOpenResponse struct {
	SessionID  string          `json:"session_id"`
	ControlURL string          `json:"control_url"`
	Media      mediaDescriptor `json:"media"`
	ExpiresAt  string          `json:"expires_at"`
}

type mediaDescriptor struct {
	PublishURL  string `json:"publish_url"`
	PublishAuth string `json:"publish_auth"`
}

func generateSessionID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "sess_" + hex.EncodeToString(b), nil
}

func deriveControlURL(backendURL, sessID string) (string, error) {
	u, err := url.Parse(backendURL)
	if err != nil {
		return "", err
	}
	host := u.Host
	if host == "" {
		return "", errInvalidBackend
	}
	scheme := "wss"
	if u.Scheme == "http" {
		scheme = "ws"
	}
	return scheme + "://" + host + "/v1/cap/" + sessID + "/control", nil
}

func derivePublishURL(backendURL, sessID string) (string, error) {
	u, err := url.Parse(backendURL)
	if err != nil {
		return "", err
	}
	host := u.Host
	if host == "" {
		return "", errInvalidBackend
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + host + "/media/" + sessID, nil
}

var errInvalidBackend = errors.New("backend.url has empty host; cannot derive session URLs")
