package poolcontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-reconciler/internal/types"
)

type Client struct {
	baseURL string
	token   string
	client  *http.Client
}

type WorkReceipt struct {
	ID                string `json:"id"`
	CreatedAt         string `json:"created_at"`
	RequestID         string `json:"request_id"`
	CapabilityID      string `json:"capability_id"`
	OfferingID        string `json:"offering_id"`
	MemberEthAddress  string `json:"member_eth_address"`
	BackendID         string `json:"backend_id"`
	ExpectedMaxUnits  uint64 `json:"expected_max_units,omitempty"`
	ActualUnits       uint64 `json:"actual_units,omitempty"`
	GatewayRevenueWei string `json:"gateway_revenue_wei,omitempty"`
	Status            string `json:"status"`
}

type ListWorkReceiptsOptions struct {
	RoundID string
	Status  string
	Limit   int
}

func NewClient(cfg config.PoolController) (*Client, error) {
	base := strings.TrimRight(cfg.URL, "/")
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	token, err := resolveBearerToken(cfg.BearerTokenRef)
	if err != nil {
		return nil, err
	}
	timeout := time.Duration(cfg.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	return &Client{
		baseURL: u.String(),
		token:   token,
		client:  &http.Client{Timeout: timeout},
	}, nil
}

func (c *Client) SubmitRoundClose(ctx context.Context, req types.RoundCloseRequest) error {
	raw, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.adminURL("/admin/v1/round-close", nil), bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("submit round close: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("pool-controller returned status %d", resp.StatusCode)
	}
	return nil
}

func (c *Client) ListWorkReceipts(ctx context.Context, opts ListWorkReceiptsOptions) ([]WorkReceipt, error) {
	query := url.Values{}
	if opts.RoundID != "" {
		query.Set("round_id", opts.RoundID)
	}
	if opts.Status != "" {
		query.Set("status", opts.Status)
	}
	if opts.Limit > 0 {
		query.Set("limit", fmt.Sprintf("%d", opts.Limit))
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.adminURL("/admin/v1/work-receipts", query), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if c.token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("list work receipts: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("pool-controller returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Receipts []WorkReceipt `json:"receipts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode work receipts: %w", err)
	}
	return payload.Receipts, nil
}

func (c *Client) adminURL(path string, query url.Values) string {
	base, _ := url.Parse(c.baseURL)
	u := base.ResolveReference(&url.URL{Path: path})
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	return u.String()
}

func resolveBearerToken(ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	key := strings.TrimPrefix(ref, "env://")
	if key == "" {
		return "", fmt.Errorf("bearer token ref must not be empty")
	}
	value, ok := os.LookupEnv(key)
	if !ok {
		return "", fmt.Errorf("env var %q is not set", key)
	}
	if value == "" {
		return "", fmt.Errorf("env var %q is empty", key)
	}
	return value, nil
}
