package receipts

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
)

type WorkReceipt struct {
	ID                string    `json:"id"`
	CreatedAt         time.Time `json:"created_at,omitempty"`
	RoundID           string    `json:"round_id,omitempty"`
	RequestID         string    `json:"request_id"`
	CapabilityID      string    `json:"capability_id"`
	OfferingID        string    `json:"offering_id"`
	MemberEthAddress  string    `json:"member_eth_address"`
	BackendID         string    `json:"backend_id"`
	ExpectedMaxUnits  uint64    `json:"expected_max_units,omitempty"`
	ActualUnits       uint64    `json:"actual_units,omitempty"`
	GatewayRevenueWei string    `json:"gateway_revenue_wei,omitempty"`
	Status            string    `json:"status"`
}

type Client interface {
	UpsertWorkReceipt(ctx context.Context, receipt WorkReceipt) error
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
	endpoint := u.ResolveReference(&url.URL{Path: "/admin/v1/work-receipts"}).String()
	return &HTTPClient{
		endpoint: endpoint,
		client:   &http.Client{Timeout: timeout},
		auth:     auth,
		cfg:      cfg,
	}, nil
}

func (c *HTTPClient) UpsertWorkReceipt(ctx context.Context, receipt WorkReceipt) error {
	body, err := json.Marshal(receipt)
	if err != nil {
		return fmt.Errorf("marshal receipt: %w", err)
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
		return fmt.Errorf("post receipt: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("receipt sink returned status %d", resp.StatusCode)
	}
	return nil
}
