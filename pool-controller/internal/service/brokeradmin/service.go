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
	State     string    `json:"state"`
	LastSeen  time.Time `json:"last_seen"`
	PublicURL string    `json:"public_url,omitempty"`
	Hardware  []struct {
		GPUUUID   string            `json:"gpu_uuid"`
		GPUModel  string            `json:"gpu_model"`
		VRAMBytes uint64            `json:"vram_bytes"`
		Driver    string            `json:"driver,omitempty"`
		CUDA      string            `json:"cuda,omitempty"`
		Facts     map[string]string `json:"facts,omitempty"`
		Kind      string            `json:"kind,omitempty"`
		Cores     int               `json:"cores,omitempty"`
		Threads   int               `json:"threads,omitempty"`
		ISA       []string          `json:"isa,omitempty"`
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
