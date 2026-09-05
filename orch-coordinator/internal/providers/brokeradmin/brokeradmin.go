// Package brokeradmin is the coordinator's client for the broker admin
// API (livepeer-network-protocol/protocols/broker-admin.md).
//
// This is the hot-zone surface: unlike the scrape client, which reads an
// unpaid public endpoint, everything here is authenticated and some of
// it writes. A write changes what a runner may serve or who may attach —
// never a price, and never the manifest, which only the cold key can
// change.
//
// The coordinator holds one bearer per broker, resolved from
// coordinator-config `brokers[].admin_token_ref`. A broker with no token
// configured is simply not administrable from here; its pages say so
// rather than failing the whole view.
package brokeradmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Errors callers switch on.
var (
	// ErrNoToken means this broker has no admin_token_ref configured.
	ErrNoToken = errors.New("brokeradmin: no admin token configured for this broker")
	// ErrUnauthorized is a rejected bearer.
	ErrUnauthorized = errors.New("brokeradmin: unauthorized")
	// ErrUnavailable is a transport failure or 5xx.
	ErrUnavailable = errors.New("brokeradmin: broker unreachable")
)

// APIError carries the broker's own error body (broker-admin §1), so the
// console can show the code and the field it named rather than a status.
type APIError struct {
	Status  int    `json:"-"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Field   string `json:"field,omitempty"`
}

func (e *APIError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("%s (%s): %s", e.Code, e.Field, e.Message)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// --- wire types ------------------------------------------------------------

// Reason is one rejection or mismatch, always naming both sides.
type Reason struct {
	Code     string `json:"code"`
	Field    string `json:"field,omitempty"`
	Declared string `json:"declared,omitempty"`
	Expected string `json:"expected,omitempty"`
	Frozen   string `json:"frozen,omitempty"`
	Message  string `json:"message,omitempty"`
}

// Runner is one attached (or recently detached) host.
type Runner struct {
	HostID     string `json:"host_id"`
	Enrollment struct {
		CredentialID     string `json:"credential_id"`
		Label            string `json:"label,omitempty"`
		MemberEthAddress string `json:"member_eth_address,omitempty"`
	} `json:"enrollment"`
	State           string             `json:"state"`
	ConnectedSince  time.Time          `json:"connected_since"`
	LastSeen        time.Time          `json:"last_seen"`
	Connections     int                `json:"connections"`
	AgentVersion    string             `json:"agent_version,omitempty"`
	ContractVersion string             `json:"contract_version,omitempty"`
	Hardware        []Hardware         `json:"hardware"`
	Capabilities    []RunnerCapability `json:"capabilities"`
}

type Hardware struct {
	GPUUUID   string            `json:"gpu_uuid"`
	GPUModel  string            `json:"gpu_model"`
	VRAMBytes uint64            `json:"vram_bytes"`
	Driver    string            `json:"driver,omitempty"`
	CUDA      string            `json:"cuda,omitempty"`
	Facts     map[string]string `json:"facts,omitempty"`
}

type RunnerCapability struct {
	LocalID      string         `json:"local_id"`
	CapabilityID string         `json:"capability_id"`
	Protocol     string         `json:"protocol,omitempty"`
	Attach       AttachVerdict  `json:"attach"`
	Declared     map[string]any `json:"declared,omitempty"`
	Offers       []OfferPair    `json:"offers"`
	Extensions   map[string]any `json:"extensions,omitempty"`
}

type AttachVerdict struct {
	Status   string   `json:"status"`
	Reasons  []Reason `json:"reasons,omitempty"`
	Warnings []Reason `json:"warnings,omitempty"`
}

// OfferPair is one offer's verdict on one runner capability.
type OfferPair struct {
	OfferingID    string    `json:"offering_id"`
	State         string    `json:"state"`
	Since         time.Time `json:"since"`
	Reason        *Reason   `json:"reason,omitempty"`
	Certification *struct {
		RunID      string    `json:"run_id,omitempty"`
		State      string    `json:"state"`
		FinishedAt time.Time `json:"finished_at,omitempty"`
	} `json:"certification,omitempty"`
}

// Offer is one offer as the broker reports it.
type Offer struct {
	OfferingID   string `json:"offering_id"`
	CapabilityID string `json:"capability_id"`
	Protocol     string `json:"protocol"`
	State        string `json:"state"`
	Advertised   bool   `json:"advertised"`
	Source       string `json:"source"`
	Operator     struct {
		Match map[string]string `json:"match,omitempty"`
		Price struct {
			AmountWei string `json:"amount_wei"`
			PerUnits  uint64 `json:"per_units"`
		} `json:"price"`
		Capacity struct {
			MaxInFlight int `json:"max_in_flight,omitempty"`
			QueueLimit  int `json:"queue_limit,omitempty"`
		} `json:"capacity,omitempty"`
		Extra           map[string]any `json:"extra,omitempty"`
		ExtraFromRunner []string       `json:"extra_from_runner,omitempty"`
		Certification   []struct {
			Name     string `json:"name"`
			Type     string `json:"type"`
			Required *bool  `json:"required,omitempty"`
		} `json:"certification,omitempty"`
	} `json:"operator"`
	Frozen     *FrozenShape `json:"frozen,omitempty"`
	Pending    *FrozenShape `json:"pending,omitempty"`
	Candidates []Candidate  `json:"candidates"`
	Runners    struct {
		Eligible   int `json:"eligible"`
		Ineligible int `json:"ineligible"`
		Matched    int `json:"matched"`
		Attached   int `json:"attached"`
	} `json:"runners"`
	AdvertisedTuple map[string]any `json:"advertised_tuple,omitempty"`
}

type FrozenShape struct {
	ShapeHash string    `json:"shape_hash"`
	FrozenAt  time.Time `json:"frozen_at"`
	FrozenBy  struct {
		HostID  string `json:"host_id"`
		LocalID string `json:"local_id"`
		RunID   string `json:"run_id,omitempty"`
	} `json:"frozen_by"`
	Projection map[string]any `json:"projection"`
}

// Candidate is a certified shape that disagrees with the frozen one —
// what an accept-shape gesture would adopt.
type Candidate struct {
	ShapeHash string `json:"shape_hash"`
	FirstSeen string `json:"first_seen"`
	Runners   []struct {
		HostID  string `json:"host_id"`
		LocalID string `json:"local_id"`
	} `json:"runners"`
	Diff       []Reason       `json:"diff"`
	Projection map[string]any `json:"projection"`
}

// CertificationRun is one run's record.
type CertificationRun struct {
	HostID     string    `json:"host_id"`
	LocalID    string    `json:"local_id"`
	OfferingID string    `json:"offering_id"`
	RunID      string    `json:"run_id"`
	Trigger    string    `json:"trigger"`
	State      string    `json:"state"`
	StartedAt  time.Time `json:"started_at"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	ShapeHash  string    `json:"shape_hash,omitempty"`
	Steps      []struct {
		Name       string         `json:"name"`
		Type       string         `json:"type"`
		Required   bool           `json:"required"`
		Status     string         `json:"status"`
		DurationMS int64          `json:"duration_ms"`
		Evidence   map[string]any `json:"evidence,omitempty"`
		Message    string         `json:"message,omitempty"`
	} `json:"steps"`
}

// Enrollment is the one-time credential response.
type Enrollment struct {
	CredentialID string `json:"credential_id"`
	HostID       string `json:"host_id"`
	Credential   struct {
		Kind  string `json:"kind"`
		Token string `json:"token"`
	} `json:"credential"`
	ExpiresAt time.Time `json:"expires_at"`
	Bundle    struct {
		BrokerURLs       map[string]string `json:"broker_urls"`
		BrokerEthAddress string            `json:"broker_eth_address"`
		ContractVersion  string            `json:"contract_version"`
	} `json:"bundle"`
}

// Credential is one stored enrollment (never a secret).
type Credential struct {
	CredentialID string    `json:"credential_id"`
	HostID       string    `json:"host_id"`
	Label        string    `json:"label,omitempty"`
	Kind         string    `json:"kind"`
	State        string    `json:"state"`
	Source       string    `json:"source"`
	IssuedAt     time.Time `json:"issued_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// --- client ----------------------------------------------------------------

// Client is the interface the console depends on; tests inject a fake.
type Client interface {
	Runners(ctx context.Context, broker string) ([]Runner, error)
	Offers(ctx context.Context, broker string) ([]Offer, error)
	AcceptShape(ctx context.Context, broker, offeringID, shapeHash string) error
	Certification(ctx context.Context, broker string) ([]CertificationRun, error)
	RunCertification(ctx context.Context, broker, hostID, offeringID, localID string) (string, error)
	Enroll(ctx context.Context, broker, hostID, label string) (*Enrollment, error)
	Credentials(ctx context.Context, broker string) ([]Credential, error)
	Revoke(ctx context.Context, broker, credentialID, reason string) error
	Disconnect(ctx context.Context, broker, hostID string) error
}

// Target is one administrable broker.
type Target struct {
	Name    string
	BaseURL string
	Token   string // empty means not administrable
}

// HTTPClient is the production client.
type HTTPClient struct {
	HTTP    *http.Client
	targets map[string]Target
}

// New builds a client over the configured targets.
func New(timeout time.Duration, targets []Target) *HTTPClient {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	byName := make(map[string]Target, len(targets))
	for _, t := range targets {
		byName[t.Name] = t
	}
	return &HTTPClient{HTTP: &http.Client{Timeout: timeout}, targets: byName}
}

// Administrable reports whether a broker has a token configured.
func (c *HTTPClient) Administrable(broker string) bool {
	t, ok := c.targets[broker]
	return ok && t.Token != ""
}

func (c *HTTPClient) do(ctx context.Context, broker, method, path string, body, out any) error {
	target, ok := c.targets[broker]
	if !ok || target.Token == "" {
		return ErrNoToken
	}
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(target.BaseURL, "/")+path, rdr)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	req.Header.Set("Authorization", "Bearer "+target.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("%w: read body: %v", ErrUnavailable, err)
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return ErrUnauthorized
	case resp.StatusCode >= 500:
		return fmt.Errorf("%w: HTTP %d", ErrUnavailable, resp.StatusCode)
	case resp.StatusCode >= 400:
		apiErr := &APIError{Status: resp.StatusCode}
		if err := json.Unmarshal(raw, apiErr); err != nil || apiErr.Code == "" {
			apiErr.Code = fmt.Sprintf("http_%d", resp.StatusCode)
			apiErr.Message = strings.TrimSpace(string(raw))
		}
		return apiErr
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("brokeradmin: decode %s: %w", path, err)
	}
	return nil
}

func (c *HTTPClient) Runners(ctx context.Context, broker string) ([]Runner, error) {
	var out struct {
		Runners []Runner `json:"runners"`
	}
	if err := c.do(ctx, broker, http.MethodGet, "/admin/v1/runners", nil, &out); err != nil {
		return nil, err
	}
	return out.Runners, nil
}

func (c *HTTPClient) Offers(ctx context.Context, broker string) ([]Offer, error) {
	var out struct {
		Offers []Offer `json:"offers"`
	}
	if err := c.do(ctx, broker, http.MethodGet, "/admin/v1/offers", nil, &out); err != nil {
		return nil, err
	}
	return out.Offers, nil
}

func (c *HTTPClient) AcceptShape(ctx context.Context, broker, offeringID, shapeHash string) error {
	return c.do(ctx, broker, http.MethodPost,
		"/admin/v1/offers/"+urlPathEscape(offeringID)+"/accept-shape",
		map[string]string{"shape_hash": shapeHash}, nil)
}

func (c *HTTPClient) Certification(ctx context.Context, broker string) ([]CertificationRun, error) {
	var out struct {
		Results []CertificationRun `json:"results"`
	}
	if err := c.do(ctx, broker, http.MethodGet, "/admin/v1/certification?latest=true", nil, &out); err != nil {
		return nil, err
	}
	return out.Results, nil
}

func (c *HTTPClient) RunCertification(ctx context.Context, broker, hostID, offeringID, localID string) (string, error) {
	var out struct {
		RunID string `json:"run_id"`
	}
	body := map[string]string{}
	if localID != "" {
		body["local_id"] = localID
	}
	err := c.do(ctx, broker, http.MethodPost,
		"/admin/v1/certification/"+urlPathEscape(hostID)+"/"+urlPathEscape(offeringID)+"/run", body, &out)
	return out.RunID, err
}

func (c *HTTPClient) Enroll(ctx context.Context, broker, hostID, label string) (*Enrollment, error) {
	body := map[string]any{}
	if hostID != "" {
		body["host_id"] = hostID
	}
	if label != "" {
		body["label"] = label
	}
	var out Enrollment
	if err := c.do(ctx, broker, http.MethodPost, "/admin/v1/enroll", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *HTTPClient) Credentials(ctx context.Context, broker string) ([]Credential, error) {
	var out struct {
		Credentials []Credential `json:"credentials"`
	}
	if err := c.do(ctx, broker, http.MethodGet, "/admin/v1/credentials", nil, &out); err != nil {
		return nil, err
	}
	return out.Credentials, nil
}

func (c *HTTPClient) Revoke(ctx context.Context, broker, credentialID, reason string) error {
	return c.do(ctx, broker, http.MethodPost,
		"/admin/v1/credentials/"+urlPathEscape(credentialID)+"/revoke",
		map[string]string{"reason": reason}, nil)
}

func (c *HTTPClient) Disconnect(ctx context.Context, broker, hostID string) error {
	return c.do(ctx, broker, http.MethodPost,
		"/admin/v1/runners/"+urlPathEscape(hostID)+"/disconnect", nil, nil)
}

// urlPathEscape keeps an id with a slash or space from changing which
// route it hits.
func urlPathEscape(s string) string {
	return strings.NewReplacer("/", "%2F", " ", "%20", "?", "%3F", "#", "%23").Replace(s)
}

var _ Client = (*HTTPClient)(nil)
