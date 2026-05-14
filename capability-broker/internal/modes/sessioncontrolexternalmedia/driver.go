package sessioncontrolexternalmedia

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes"
)

// Mode is the canonical mode-name@vN string for this driver.
const Mode = "session-control-external-media@v0"

// DefaultExpiresIn is the no-attach deadline window. Matches
// session-control-plus-media@v0.
const DefaultExpiresIn = 1 * time.Hour

// Default knobs for the control-WS lifecycle plumbing.
const (
	DefaultHeartbeatInterval        = 10 * time.Second
	DefaultMissedHeartbeatThreshold = 3
	DefaultBackpressureDropAfter    = 5 * time.Second
	DefaultOutboundBufferMessages   = 64
	DefaultHandshakeTimeout         = 10 * time.Second

	// DefaultUsageTickCadence is how often the broker emits
	// session.usage.tick frames on the control-WS. Matches the
	// payment middleware's debit cadence.
	DefaultUsageTickCadence = 5 * time.Second

	// DefaultBackendUnresponsiveTimeout is the window the proxy may
	// observe consecutive backend failures before emitting
	// session.error{code=runner_disconnect}.
	DefaultBackendUnresponsiveTimeout = 30 * time.Second
)

// Config holds the driver's tunables.
type Config struct {
	HeartbeatInterval         time.Duration
	MissedHeartbeatThreshold  int
	BackpressureDropAfter     time.Duration
	OutboundBufferMessages    int
	HandshakeTimeout          time.Duration
	UsageTickCadence          time.Duration
	BackendUnresponsiveTimeout time.Duration
}

// DefaultConfig returns the recommended defaults.
func DefaultConfig() Config {
	return Config{
		HeartbeatInterval:          DefaultHeartbeatInterval,
		MissedHeartbeatThreshold:   DefaultMissedHeartbeatThreshold,
		BackpressureDropAfter:      DefaultBackpressureDropAfter,
		OutboundBufferMessages:     DefaultOutboundBufferMessages,
		HandshakeTimeout:           DefaultHandshakeTimeout,
		UsageTickCadence:           DefaultUsageTickCadence,
		BackendUnresponsiveTimeout: DefaultBackendUnresponsiveTimeout,
	}
}

// Driver implements modes.Driver for session-control-external-media@v0.
// It hosts:
//   - POST /v1/cap                            (modes.Driver.Serve)
//   - GET  /v1/cap/{session_id}/control       (Driver.ServeControlWS)
//   - *    /_scope/{session_id}/{path...}     (Driver.ServeProxy)
type Driver struct {
	store    *Store
	cfg      Config
	upgrader websocket.Upgrader
	client   *http.Client
}

// Compile-time interface check.
var _ modes.Driver = (*Driver)(nil)

// New returns a driver bound to a session store + config.
func New(store *Store, cfg Config) *Driver {
	if cfg.HandshakeTimeout <= 0 {
		cfg.HandshakeTimeout = DefaultHandshakeTimeout
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if cfg.UsageTickCadence <= 0 {
		cfg.UsageTickCadence = DefaultUsageTickCadence
	}
	if cfg.BackendUnresponsiveTimeout <= 0 {
		cfg.BackendUnresponsiveTimeout = DefaultBackendUnresponsiveTimeout
	}
	if cfg.OutboundBufferMessages <= 0 {
		cfg.OutboundBufferMessages = DefaultOutboundBufferMessages
	}
	return &Driver{
		store: store,
		cfg:   cfg,
		upgrader: websocket.Upgrader{
			HandshakeTimeout: cfg.HandshakeTimeout,
			CheckOrigin:      func(r *http.Request) bool { return true },
		},
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Store returns the driver's session store. Exposed for the composition
// root (the mux registers handlers that read it).
func (d *Driver) Store() *Store { return d.store }

// Config returns the driver's config (test helper).
func (d *Driver) Config() Config { return d.cfg }

// Mode returns the mode identifier.
func (d *Driver) Mode() string { return Mode }

// Serve responds to the session-open POST and registers the session in
// the store. The control-WS upgrade handler and the /_scope reverse
// proxy are registered separately on the broker mux at server
// construction time.
func (d *Driver) Serve(_ context.Context, p modes.Params) error {
	if p.Request.Method != http.MethodPost {
		livepeerheader.WriteError(p.Writer, http.StatusMethodNotAllowed,
			livepeerheader.ErrModeUnsupported,
			Mode+" session-open is POST")
		return nil
	}

	sessID, err := generateSessionID()
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError,
			livepeerheader.ErrInternalError, "session id: "+err.Error())
		return nil
	}

	base := p.Capability.Backend.URL
	if base == "" {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError,
			livepeerheader.ErrInternalError, "backend.url is required for "+Mode)
		return nil
	}

	ctrlURL, err := deriveControlURL(p.Request, sessID)
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError,
			livepeerheader.ErrInternalError, "control_url: "+err.Error())
		return nil
	}
	scopeURL, err := deriveScopeURL(p.Request, sessID)
	if err != nil {
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError,
			livepeerheader.ErrInternalError, "scope_url: "+err.Error())
		return nil
	}

	mediaSchema, _ := p.Capability.Extra["media_schema"].(string)
	if mediaSchema == "" {
		mediaSchema = "scope-passthrough/v0"
	}
	startPath, _ := p.Capability.Extra["session_start_path"].(string)
	stopPath, _ := p.Capability.Extra["session_stop_path"].(string)

	now := time.Now().UTC()
	ctx, cancel := context.WithCancel(context.Background())
	rec := &SessionRecord{
		SessionID:        sessID,
		CapabilityID:     p.Capability.ID,
		OfferingID:       p.Capability.OfferingID,
		BackendURL:       base,
		SessionStartPath: startPath,
		SessionStopPath:  stopPath,
		OpenedAt:         now,
		ExpiresAt:        now.Add(DefaultExpiresIn),
		LiveCounter:      p.LiveCounter,
		Cancel:           cancel,
	}
	if err := d.store.Add(rec); err != nil {
		cancel()
		livepeerheader.WriteError(p.Writer, http.StatusInternalServerError,
			livepeerheader.ErrInternalError, "session store: "+err.Error())
		return nil
	}

	go d.runUsageTicker(ctx, rec)

	body := sessionOpenResponse{
		SessionID:  sessID,
		ControlURL: ctrlURL,
		Media: mediaDescriptor{
			Schema:   mediaSchema,
			ScopeURL: scopeURL,
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

// runUsageTicker emits session.usage.tick on cadence and tears the
// session down when LiveCounter or backend signal terminal failure.
// Runs for the lifetime of the session record.
func (d *Driver) runUsageTicker(ctx context.Context, rec *SessionRecord) {
	t := time.NewTicker(d.cfg.UsageTickCadence)
	defer t.Stop()
	defer d.teardown(rec, "context_done")
	var lastUnits uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if rec.Closed() {
				return
			}
			var units uint64
			if rec.LiveCounter != nil {
				units = rec.LiveCounter.CurrentUnits()
			}
			if units == lastUnits {
				continue
			}
			lastUnits = units
			d.emitUsageTick(rec, units)
		}
	}
}

// teardown is the idempotent terminal path. Sends session.ended,
// cancels session goroutines, removes from the store, attempts a
// best-effort backend stop call via the capability-declared
// session_stop_path.
func (d *Driver) teardown(rec *SessionRecord, reason string) {
	if rec.MarkClosed() {
		return
	}
	d.emitSessionEnded(rec, reason)
	if rec.Cancel != nil {
		rec.Cancel()
	}
	if rec.SessionStopPath != "" {
		go d.callBackendStop(rec)
	}
	d.store.Remove(rec.SessionID)
}

// callBackendStop best-effort POSTs to the capability-declared stop
// path on the workload backend. Errors are logged but not surfaced —
// the session is gone either way.
func (d *Driver) callBackendStop(rec *SessionRecord) {
	stopURL := strings.TrimRight(rec.BackendURL, "/") + rec.SessionStopPath
	req, err := http.NewRequest(http.MethodPost, stopURL, nil)
	if err != nil {
		return
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

type sessionOpenResponse struct {
	SessionID  string          `json:"session_id"`
	ControlURL string          `json:"control_url"`
	Media      mediaDescriptor `json:"media"`
	ExpiresAt  string          `json:"expires_at"`
}

type mediaDescriptor struct {
	Schema   string `json:"schema"`
	ScopeURL string `json:"scope_url"`
}

func generateSessionID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "sess_" + hex.EncodeToString(b), nil
}

// deriveControlURL builds the wss:// URL the gateway should dial. We
// reuse the inbound request's Host so deployments behind a reverse
// proxy resolve to the externally-routable hostname.
func deriveControlURL(r *http.Request, sessID string) (string, error) {
	host := r.Host
	if host == "" {
		return "", errInvalidRequest
	}
	scheme := "wss"
	if r.TLS == nil {
		scheme = "ws"
	}
	return scheme + "://" + host + "/v1/cap/" + sessID + "/control", nil
}

// deriveScopeURL builds the externally-routable URL of the broker's
// /_scope reverse proxy for this session.
func deriveScopeURL(r *http.Request, sessID string) (string, error) {
	host := r.Host
	if host == "" {
		return "", errInvalidRequest
	}
	scheme := "https"
	if r.TLS == nil {
		scheme = "http"
	}
	return scheme + "://" + host + "/_scope/" + sessID + "/", nil
}

// scopeURLPath returns the path the proxy is mounted at for a given
// session id, including a trailing slash. Helper for handler
// registration + tests.
func scopeURLPath(sessID string) string {
	return "/_scope/" + sessID + "/"
}

var errInvalidRequest = errors.New("request has empty Host header; cannot derive session URLs")
