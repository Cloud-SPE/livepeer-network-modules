// Package sessioncontrolplusmedia implements the
// session-control-plus-media@v0 interaction-mode driver per
// livepeer-network-protocol/modes/session-control-plus-media.md.
//
// v0.1 NARROW SCOPE: session-open phase only. Returns 202 with
// session_id / control_url / media.{publish_url, publish_auth} /
// expires_at. The control-plane WebSocket lifecycle and media-plane
// provisioning are deferred to a follow-up plan.
//
// See plan 0012 for the explicit out-of-scope list.
package sessioncontrolplusmedia

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
const Mode = "session-control-plus-media@v0"

// Driver implements modes.Driver.
type Driver struct{}

// Compile-time interface check.
var _ modes.Driver = (*Driver)(nil)

// New returns a stateless session-control-plus-media driver.
func New() *Driver { return &Driver{} }

// Mode returns the mode identifier.
func (d *Driver) Mode() string { return Mode }

// Serve responds to the session-open POST with the required body fields.
func (d *Driver) Serve(ctx context.Context, p modes.Params) error {
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

	body := sessionOpenResponse{
		SessionID:  sessID,
		ControlURL: ctrlURL,
		Media: mediaDescriptor{
			PublishURL:  pubURL,
			PublishAuth: "stub-publish-auth-" + sessID, // placeholder for v0.1
		},
		ExpiresAt: time.Now().UTC().Add(1 * time.Hour).Format(time.RFC3339),
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

var errInvalidBackend = errInvalidBackendURL{}

type errInvalidBackendURL struct{}

func (errInvalidBackendURL) Error() string {
	return "backend.url has empty host; cannot derive session URLs"
}
