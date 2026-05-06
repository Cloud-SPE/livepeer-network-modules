// Package rtmpingresshlsegress implements the rtmp-ingress-hls-egress@v0
// interaction-mode driver per
// livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md.
//
// v0.1 NARROW SCOPE: session-open phase only. The broker accepts the
// session-open POST, validates payment + headers via the standard
// middleware, and returns 202 with the required URL set
// (rtmp_ingest_url / hls_playback_url / control_url / expires_at). The
// actual RTMP listener, FFmpeg transcoding, and HLS sink are deferred
// to a follow-up plan once the gateway side has integration tests
// against this wire shape.
//
// See plan 0011 for the explicit out-of-scope list.
package rtmpingresshlsegress

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes"
)

// Mode is the canonical mode-name@vN string for this driver.
const Mode = "rtmp-ingress-hls-egress@v0"

// Driver implements modes.Driver.
type Driver struct{}

// Compile-time interface check.
var _ modes.Driver = (*Driver)(nil)

// New returns a stateless rtmp-ingress-hls-egress driver.
func New() *Driver { return &Driver{} }

// Mode returns the mode identifier.
func (d *Driver) Mode() string { return Mode }

// Serve responds to the session-open POST with the required URL set.
//
// v0.1: URLs are derived from the configured backend.url (treated as the
// base of the broker's external advertised host). The actual media plane
// is not stood up — runners that test the session-open wire shape see a
// well-formed 202 response.
func (d *Driver) Serve(ctx context.Context, p modes.Params) error {
	if p.Request.Method != http.MethodPost {
		livepeerheader.WriteError(p.Writer, http.StatusMethodNotAllowed, livepeerheader.ErrModeUnsupported,
			"rtmp-ingress-hls-egress@v0 session-open is POST")
		return nil
	}

	sessID, err := generateSessionID()
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"session id: "+err.Error())
		return nil
	}

	base := p.Capability.Backend.URL
	rtmpURL, err := deriveRTMPIngestURL(base, sessID)
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"rtmp_ingest_url: "+err.Error())
		return nil
	}
	hlsURL, err := deriveHLSPlaybackURL(base, sessID)
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"hls_playback_url: "+err.Error())
		return nil
	}
	ctrlURL, err := deriveControlURL(base, sessID)
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"control_url: "+err.Error())
		return nil
	}

	body := sessionOpenResponse{
		SessionID:       sessID,
		RTMPIngestURL:   rtmpURL,
		HLSPlaybackURL:  hlsURL,
		ControlURL:      ctrlURL,
		ExpiresAt:       time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339),
	}
	encoded, _ := json.Marshal(body)

	p.Writer.Header().Set("Content-Type", "application/json")
	// v0.1 narrowed scope: no work units consumed at session-open. Set
	// explicitly so Payment middleware reconciles to zero.
	p.Writer.Header().Set(livepeerheader.WorkUnits, "0")
	p.Writer.WriteHeader(http.StatusAccepted)
	_, _ = p.Writer.Write(encoded)
	return nil
}

type sessionOpenResponse struct {
	SessionID      string `json:"session_id"`
	RTMPIngestURL  string `json:"rtmp_ingest_url"`
	HLSPlaybackURL string `json:"hls_playback_url"`
	ControlURL     string `json:"control_url"`
	ExpiresAt      string `json:"expires_at"`
}

func generateSessionID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "sess_" + hex.EncodeToString(b), nil
}

func deriveRTMPIngestURL(backendURL, sessID string) (string, error) {
	u, err := url.Parse(backendURL)
	if err != nil {
		return "", err
	}
	host := u.Hostname()
	if host == "" {
		return "", errInvalidBackend
	}
	// Default RTMP port. v0.1 doesn't actually listen here.
	return "rtmp://" + host + ":1935/" + sessID, nil
}

func deriveHLSPlaybackURL(backendURL, sessID string) (string, error) {
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
	return scheme + "://" + host + "/hls/" + sessID + "/manifest.m3u8", nil
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

var errInvalidBackend = errInvalidBackendURL{}

type errInvalidBackendURL struct{}

func (errInvalidBackendURL) Error() string {
	return "backend.url has empty host; cannot derive session URLs"
}
