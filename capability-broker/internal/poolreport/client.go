package poolreport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/observability"
)

const outcomesPath = "/admin/v1/backend-outcomes"

const (
	OutcomeSuccess        = "success"
	OutcomeBackendFailure = "backend_failure"
	OutcomeCallerFailure  = "caller_failure"
)

type BackendOutcome struct {
	BackendID        string    `json:"backend_id"`
	CapabilityID     string    `json:"capability_id"`
	OfferingID       string    `json:"offering_id"`
	MemberEthAddress string    `json:"member_eth_address"`
	Outcome          string    `json:"outcome"`
	LatencyMetricMS  int64     `json:"latency_metric_ms"`
	OccurredAt       time.Time `json:"occurred_at"`
}

type Client interface {
	ReportBackendOutcome(ctx context.Context, outcome BackendOutcome) error
}

type HTTPClient struct {
	endpoint string
	client   *http.Client
	auth     *backend.AuthApplier
	cfg      config.AuthConfig
}

func NewHTTPClient(baseURL string, timeout time.Duration, cfg config.AuthConfig, auth *backend.AuthApplier) (*HTTPClient, error) {
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	baseURL = strings.TrimRight(baseURL, "/")
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, err
	}
	endpoint := u.ResolveReference(&url.URL{Path: outcomesPath}).String()
	return &HTTPClient{
		endpoint: endpoint,
		client:   &http.Client{Timeout: timeout},
		auth:     auth,
		cfg:      cfg,
	}, nil
}

func (c *HTTPClient) ReportBackendOutcome(ctx context.Context, outcome BackendOutcome) error {
	body, err := json.Marshal(outcome)
	if err != nil {
		return fmt.Errorf("marshal backend outcome: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.auth != nil {
		if err := c.auth.Apply(req.Header, c.cfg); err != nil {
			return fmt.Errorf("apply auth: %w", err)
		}
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("post backend outcome: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("backend outcome sink returned status %d", resp.StatusCode)
	}
	return nil
}

func ReportBestEffort(client Client, outcome BackendOutcome) {
	if client == nil {
		return
	}
	go func() {
		if err := client.ReportBackendOutcome(context.Background(), outcome); err != nil {
			observability.RecordBackendOutcomeEmit(outcome.Outcome, "error")
			log.Printf("warning: backend outcome emit failed backend_id=%s capability=%s offering=%s outcome=%s: %v",
				outcome.BackendID, outcome.CapabilityID, outcome.OfferingID, outcome.Outcome, err)
			return
		}
		observability.RecordBackendOutcomeEmit(outcome.Outcome, "success")
	}()
}
