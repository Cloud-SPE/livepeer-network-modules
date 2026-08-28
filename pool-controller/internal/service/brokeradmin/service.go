package brokeradmin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
)

type RuntimeStatus struct {
	LoadedRevision       string    `json:"loaded_revision,omitempty"`
	LoadedConfigPath     string    `json:"loaded_config_path,omitempty"`
	LoadedAt             time.Time `json:"loaded_at,omitempty"`
	LastReloadAttemptID  string    `json:"last_reload_attempt_id,omitempty"`
	LastReloadStartedAt  time.Time `json:"last_reload_started_at,omitempty"`
	LastReloadFinishedAt time.Time `json:"last_reload_finished_at,omitempty"`
	LastReloadStatus     string    `json:"last_reload_status,omitempty"`
	LastReloadError      string    `json:"last_reload_error,omitempty"`
}

type Client struct {
	baseURL string
	auth    config.AuthConfig
	client  *http.Client
}

func New(baseURL string, auth config.AuthConfig, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		auth:    auth,
		client:  &http.Client{Timeout: timeout},
	}
}

func (c *Client) ReloadAndConfirm(desiredRevision string) (*RuntimeStatus, error) {
	if c == nil || c.baseURL == "" {
		return nil, nil
	}
	if strings.TrimSpace(desiredRevision) == "" {
		return nil, fmt.Errorf("desired revision is required")
	}
	reloadStatus, err := c.postReload()
	if err != nil {
		return nil, err
	}
	status, err := c.getRuntime()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(reloadStatus.LastReloadAttemptID) != "" && status.LastReloadAttemptID != reloadStatus.LastReloadAttemptID {
		return status, fmt.Errorf("broker runtime last reload attempt %q does not match triggered attempt %q", status.LastReloadAttemptID, reloadStatus.LastReloadAttemptID)
	}
	if status.LoadedRevision != desiredRevision {
		return status, fmt.Errorf("broker loaded revision %q does not match desired revision %q", status.LoadedRevision, desiredRevision)
	}
	if status.LastReloadStatus != "" && status.LastReloadStatus != "applied" && status.LastReloadStatus != "startup_loaded" {
		return status, fmt.Errorf("broker runtime reload status is %q", status.LastReloadStatus)
	}
	return status, nil
}

func (c *Client) KillWorkerSession(backendID string) error {
	if c == nil || c.baseURL == "" {
		return nil
	}
	backendID = strings.TrimSpace(backendID)
	if backendID == "" {
		return fmt.Errorf("backend id is required")
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, c.baseURL+"/admin/v1/worker-sessions/"+backendID+"/kill", nil)
	if err != nil {
		return err
	}
	if err := applyAuth(req, c.auth); err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("broker worker-session kill request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("broker worker-session kill request returned status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) postReload() (*RuntimeStatus, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, c.baseURL+"/admin/v1/runtime/reload", nil)
	if err != nil {
		return nil, err
	}
	if err := applyAuth(req, c.auth); err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("broker reload request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("broker reload request returned status %d", resp.StatusCode)
	}
	var status RuntimeStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decode broker reload status: %w", err)
	}
	return &status, nil
}

func (c *Client) getRuntime() (*RuntimeStatus, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, c.baseURL+"/admin/v1/runtime", nil)
	if err != nil {
		return nil, err
	}
	if err := applyAuth(req, c.auth); err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("broker runtime request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("broker runtime request returned status %d", resp.StatusCode)
	}
	var status RuntimeStatus
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("decode broker runtime status: %w", err)
	}
	return &status, nil
}

func applyAuth(req *http.Request, auth config.AuthConfig) error {
	switch auth.Method {
	case "", "none":
		return nil
	case "bearer":
		secret := strings.TrimSpace(auth.SecretRef)
		if secret == "" {
			return fmt.Errorf("auth secret ref is required")
		}
		if strings.HasPrefix(secret, "env://") {
			key := strings.TrimPrefix(secret, "env://")
			value := strings.TrimSpace(os.Getenv(key))
			if value == "" {
				return fmt.Errorf("auth secret env var %q is empty", key)
			}
			req.Header.Set("Authorization", "Bearer "+value)
			return nil
		}
		return fmt.Errorf("unsupported auth secret ref %q", secret)
	default:
		return fmt.Errorf("unsupported auth method %q", auth.Method)
	}
}

// --- plan 0043: push offers and credentials, read runner state --------------
//
// The controller stopped rendering the broker's config file. It now
// pushes what it owns — the offer set and the credentials that may
// attach — over the broker admin API, and reads back what the broker
// owns: which runners are attached and how they certified.
//
// Both pushes are full replacements and idempotent (broker-admin §4.2,
// §5.4): a push identical to the current state changes nothing, and an
// entry that disappears from a credential push is a revoke.

// OfferPush is one offer in the shape the broker's offers[] grammar
// accepts. Field names match that grammar exactly, because the broker
// validates the push with the same decoder it uses for its config file
// and rejects unknown fields.
type OfferPush struct {
	OfferingID      string             `json:"offering_id"`
	Capability      string             `json:"capability"`
	Protocol        string             `json:"protocol"`
	Match           map[string]string  `json:"match,omitempty"`
	Price           OfferPushPrice     `json:"price"`
	Capacity        *OfferPushCapacity `json:"capacity,omitempty"`
	Extra           map[string]any     `json:"extra,omitempty"`
	Constraints     map[string]any     `json:"constraints,omitempty"`
	ExtraFromRunner []string           `json:"extra_from_runner,omitempty"`
	// SessionPolicy is the operator half of a paid-session offering.
	// Omitted for paid-job, where it has no meaning.
	SessionPolicy *OfferPushSessionPolicy `json:"session_policy,omitempty"`
	Certification []OfferPushCertStep     `json:"certification,omitempty"`
	Disabled      bool                    `json:"disabled,omitempty"`
}

type OfferPushPrice struct {
	AmountWei string `json:"amount_wei"`
	PerUnits  uint64 `json:"per_units"`
}

type OfferPushCapacity struct {
	MaxInFlight int `json:"max_in_flight,omitempty"`
	QueueLimit  int `json:"queue_limit,omitempty"`
}

// OfferPushCertStep is a certification step as the offer carries it.
// The controller authors these as POLICY — which steps must pass — and
// the broker executes them (plan 0043 decision 6b).
// OfferPushSessionPolicy mirrors the broker's offer session axes. The
// field names are the broker's own, because this is its wire shape and
// a rename here would silently drop an axis.
type OfferPushSessionPolicy struct {
	Attachment           string              `json:"attachment,omitempty"`
	Refill               string              `json:"refill,omitempty"`
	LeasePolicy          string              `json:"lease_policy,omitempty"`
	LeaseMaxSeconds      int                 `json:"lease_max_seconds,omitempty"`
	BurnRatePerSec       float64             `json:"burn_rate_per_second,omitempty"`
	MinRunwayUnits       int64               `json:"min_runway_units,omitempty"`
	MaxRotations         int                 `json:"max_rotations,omitempty"`
	ToleranceBandPct     float64             `json:"tolerance_band_pct,omitempty"`
	RunwayIncrementUnits int64               `json:"runway_increment_units,omitempty"`
	Heartbeat            *OfferPushHeartbeat `json:"heartbeat,omitempty"`
}

type OfferPushHeartbeat struct {
	IntervalSeconds int `json:"interval_seconds,omitempty"`
	MissedThreshold int `json:"missed_threshold,omitempty"`
}

type OfferPushCertStep struct {
	Name      string         `json:"name"`
	Type      string         `json:"type"`
	Required  *bool          `json:"required,omitempty"`
	TimeoutMS int            `json:"timeout_ms,omitempty"`
	Config    map[string]any `json:"config,omitempty"`
}

// CredentialPush is one credential the broker should accept. Only the
// hash travels: the controller is the minting authority and holds the
// plaintext exactly long enough to hand it to the member.
type CredentialPush struct {
	CredentialID     string    `json:"credential_id"`
	HostID           string    `json:"host_id"`
	Kind             string    `json:"kind"`
	TokenSHA256      string    `json:"token_sha256"`
	ExpiresAt        time.Time `json:"expires_at"`
	Label            string    `json:"label,omitempty"`
	MemberEthAddress string    `json:"member_eth_address,omitempty"`
	State            string    `json:"state,omitempty"`
}

// PushResult reports what a push changed.
type PushResult struct {
	Revision     string   `json:"revision"`
	Applied      bool     `json:"applied"`
	Changed      []string `json:"changed,omitempty"`
	RevokedHosts []string `json:"revoked_hosts,omitempty"`
}

// PutOffers replaces the broker's offer set.
func (c *Client) PutOffers(ctx context.Context, revision string, offers []OfferPush) (*PushResult, error) {
	if offers == nil {
		offers = []OfferPush{}
	}
	body := struct {
		Revision string      `json:"revision"`
		Offers   []OfferPush `json:"offers"`
	}{Revision: revision, Offers: offers}
	var out PushResult
	if err := c.doJSON(ctx, http.MethodPut, "/admin/v1/offers", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PutCredentials replaces the synced credential set. Entries that
// disappear are revoked, which closes their hosts' connections.
func (c *Client) PutCredentials(ctx context.Context, revision string, creds []CredentialPush) (*PushResult, error) {
	if creds == nil {
		creds = []CredentialPush{}
	}
	body := struct {
		Revision    string           `json:"revision"`
		Credentials []CredentialPush `json:"credentials"`
	}{Revision: revision, Credentials: creds}
	var out PushResult
	if err := c.doJSON(ctx, http.MethodPut, "/admin/v1/credentials", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RunnerView is the slice of the broker's runner record the controller
// needs: who is attached, and what hardware they reported. Hardware now
// reaches the controller this way rather than through a member-facing
// report — the broker already validated it against the attach contract.
type RunnerView struct {
	HostID     string `json:"host_id"`
	Enrollment struct {
		CredentialID     string `json:"credential_id"`
		Label            string `json:"label,omitempty"`
		MemberEthAddress string `json:"member_eth_address,omitempty"`
	} `json:"enrollment"`
	State    string    `json:"state"`
	LastSeen time.Time `json:"last_seen"`
	Hardware []struct {
		GPUUUID   string            `json:"gpu_uuid"`
		GPUModel  string            `json:"gpu_model"`
		VRAMBytes uint64            `json:"vram_bytes"`
		Driver    string            `json:"driver,omitempty"`
		CUDA      string            `json:"cuda,omitempty"`
		Facts     map[string]string `json:"facts,omitempty"`
	} `json:"hardware"`
	Capabilities []struct {
		LocalID      string `json:"local_id"`
		CapabilityID string `json:"capability_id"`
		Attach       struct {
			Status string `json:"status"`
		} `json:"attach"`
		Offers []struct {
			OfferingID string `json:"offering_id"`
			State      string `json:"state"`
		} `json:"offers"`
	} `json:"capabilities"`
}

// Runners reads the broker's attached-runner view.
func (c *Client) Runners(ctx context.Context) ([]RunnerView, error) {
	var out struct {
		Runners []RunnerView `json:"runners"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/admin/v1/runners", nil, &out); err != nil {
		return nil, err
	}
	return out.Runners, nil
}

// CertificationView is one run's outcome, the ladder's input.
type CertificationView struct {
	HostID     string    `json:"host_id"`
	LocalID    string    `json:"local_id"`
	OfferingID string    `json:"offering_id"`
	RunID      string    `json:"run_id"`
	State      string    `json:"state"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

// Certification reads the latest run per runner × offer.
func (c *Client) Certification(ctx context.Context) ([]CertificationView, error) {
	var out struct {
		Results []CertificationView `json:"results"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/admin/v1/certification?latest=true", nil, &out); err != nil {
		return nil, err
	}
	return out.Results, nil
}

// doJSON is the shared request path for the plan-0043 calls. It surfaces
// the broker's own error body, so a rejected push says which offer and
// which field rather than a status code.
func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.baseURL, "/")+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	if err := applyAuth(req, c.auth); err != nil {
		return err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("broker admin %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("broker admin %s %s: read body: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiErr struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Field   string `json:"field"`
		}
		if json.Unmarshal(raw, &apiErr) == nil && apiErr.Code != "" {
			if apiErr.Field != "" {
				return fmt.Errorf("broker admin %s %s: %s (%s): %s", method, path, apiErr.Code, apiErr.Field, apiErr.Message)
			}
			return fmt.Errorf("broker admin %s %s: %s: %s", method, path, apiErr.Code, apiErr.Message)
		}
		return fmt.Errorf("broker admin %s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}
