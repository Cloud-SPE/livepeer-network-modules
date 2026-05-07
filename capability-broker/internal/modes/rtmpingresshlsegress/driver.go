// Package rtmpingresshlsegress implements the rtmp-ingress-hls-egress@v0
// interaction-mode driver per
// livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md.
package rtmpingresshlsegress

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes"
)

// Mode is the canonical mode-name@vN string for this driver.
const Mode = "rtmp-ingress-hls-egress@v0"

// DefaultExpiresIn is the no-push deadline window. Spec
// recommendation: ~1 hour.
const DefaultExpiresIn = 1 * time.Hour

// Driver implements modes.Driver.
type Driver struct {
	store *Store
	cfg   Config
}

var _ modes.Driver = (*Driver)(nil)

// Config holds the broker-wide settings the driver needs at request
// time. Listener-side knobs (port, max concurrent, idle timeout,
// duplicate policy) live on internal/media/rtmp.Config.
type Config struct {
	// PublicHost is the host:port the broker advertises to customers
	// for the LL-HLS playback URL. Falls back to backend.url when
	// empty.
	PublicHost string
	// RTMPHost is the host:port the broker advertises for the RTMP
	// ingest URL. Falls back to <hostname-of-backend.url>:1935.
	RTMPHost string
	// HLSScheme is the scheme of the playback URL ("https" by
	// default; "http" for local fixtures).
	HLSScheme string
	// ExpiresIn overrides DefaultExpiresIn.
	ExpiresIn time.Duration
}

// New returns a stateful driver bound to a session store. The store is
// shared with the RTMP listener (defense-in-depth stream-key check)
// and any watchdog goroutines.
func New(store *Store, cfg Config) *Driver {
	if cfg.ExpiresIn == 0 {
		cfg.ExpiresIn = DefaultExpiresIn
	}
	if cfg.HLSScheme == "" {
		cfg.HLSScheme = "https"
	}
	return &Driver{store: store, cfg: cfg}
}

// Mode returns the mode identifier.
func (d *Driver) Mode() string { return Mode }

// Store returns the driver's session store. Exposed for the
// composition root (the listener and watchdogs read it).
func (d *Driver) Store() *Store { return d.store }

// Serve responds to the session-open POST.
func (d *Driver) Serve(_ context.Context, p modes.Params) error {
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
	streamKey, err := generateStreamKey()
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"stream key: "+err.Error())
		return nil
	}

	rtmpHost, err := d.resolveRTMPHost(p.Capability.Backend.URL)
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"rtmp_ingest_url: "+err.Error())
		return nil
	}
	publicHost, scheme, err := d.resolvePublicHost(p.Capability.Backend.URL)
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"hls_playback_url: "+err.Error())
		return nil
	}

	now := time.Now().UTC()
	rec := &SessionRecord{
		SessionID:    sessID,
		StreamKey:    streamKey,
		Profile:      p.Capability.Backend.Profile,
		CapabilityID: p.Capability.ID,
		OfferingID:   p.Capability.OfferingID,
		ExpiresAt:    now.Add(d.cfg.ExpiresIn),
		OpenedAt:     now,
	}
	if err := d.store.Add(rec); err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"session store: "+err.Error())
		return nil
	}

	body := sessionOpenResponse{
		SessionID:      sessID,
		StreamKey:      streamKey,
		RTMPIngestURL:  "rtmp://" + rtmpHost + "/" + sessID + "/" + streamKey,
		HLSPlaybackURL: scheme + "://" + publicHost + "/_hls/" + sessID + "/playlist.m3u8",
		ControlURL:     controlURL(scheme, publicHost, sessID),
		ExpiresAt:      rec.ExpiresAt.Format(time.RFC3339),
	}
	encoded, _ := json.Marshal(body)

	p.Writer.Header().Set("Content-Type", "application/json")
	p.Writer.Header().Set(livepeerheader.WorkUnits, "0")
	p.Writer.WriteHeader(http.StatusAccepted)
	_, _ = p.Writer.Write(encoded)
	return nil
}

func (d *Driver) resolveRTMPHost(backendURL string) (string, error) {
	if d.cfg.RTMPHost != "" {
		return d.cfg.RTMPHost, nil
	}
	u, err := url.Parse(backendURL)
	if err != nil {
		return "", err
	}
	host := u.Hostname()
	if host == "" {
		return "", errInvalidBackend
	}
	return host + ":1935", nil
}

func (d *Driver) resolvePublicHost(backendURL string) (string, string, error) {
	if d.cfg.PublicHost != "" {
		return d.cfg.PublicHost, d.cfg.HLSScheme, nil
	}
	u, err := url.Parse(backendURL)
	if err != nil {
		return "", "", err
	}
	host := u.Host
	if host == "" {
		return "", "", errInvalidBackend
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	return host, scheme, nil
}

func controlURL(scheme, host, sessID string) string {
	wsScheme := "wss"
	if scheme == "http" {
		wsScheme = "ws"
	}
	return wsScheme + "://" + host + "/v1/cap/" + sessID + "/control"
}

type sessionOpenResponse struct {
	SessionID      string `json:"session_id"`
	StreamKey      string `json:"stream_key"`
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

func generateStreamKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

var errInvalidBackend = errors.New("backend.url has empty host; cannot derive session URLs")
