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
	"strconv"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/config"
)

type Client struct {
	baseURL string
	token   string
	client  *http.Client
}

type PayoutIntent struct {
	ID                 string `json:"id"`
	CreatedAt          string `json:"created_at"`
	RoundReceiptID     string `json:"round_receipt_id"`
	RoundID            string `json:"round_id"`
	MemberEthAddress   string `json:"member_eth_address"`
	DestinationAddress string `json:"destination_address"`
	ChainID            uint64 `json:"chain_id"`
	Asset              string `json:"asset"`
	AmountWei          string `json:"amount_wei"`
	Status             string `json:"status"`
	ExportedAt         string `json:"exported_at,omitempty"`
	LeasedAt           string `json:"leased_at,omitempty"`
	LeaseID            string `json:"lease_id,omitempty"`
	LeaseOwner         string `json:"lease_owner,omitempty"`
	LeaseExpiresAt     string `json:"lease_expires_at,omitempty"`
	SubmittedAt        string `json:"submitted_at,omitempty"`
	PaidAt             string `json:"paid_at,omitempty"`
	FailedAt           string `json:"failed_at,omitempty"`
	RetryCount         uint64 `json:"retry_count,omitempty"`
	LastRequeuedAt     string `json:"last_requeued_at,omitempty"`
	ExternalRef        string `json:"external_ref,omitempty"`
	TxHash             string `json:"tx_hash,omitempty"`
	FailureReason      string `json:"failure_reason,omitempty"`
}

type ListPayoutIntentsOptions struct {
	RoundID          string
	MemberEthAddress string
	Status           string
	Limit            int
}

type ListPayoutAlertsOptions struct {
	RoundID                    string
	MemberEthAddress           string
	Status                     string
	Limit                      int
	SubmittedOlderThanSeconds  int
	FailedOlderThanSeconds     int
	LeaseExpiresWithinSeconds  int
	RetryCountAtLeast          int
	RecentRequeueWithinSeconds int
}

type UpdatePayoutIntentStatusRequest struct {
	IDs           []string `json:"ids"`
	Status        string   `json:"status"`
	LeaseID       string   `json:"lease_id,omitempty"`
	ExternalRef   string   `json:"external_ref,omitempty"`
	TxHash        string   `json:"tx_hash,omitempty"`
	FailureReason string   `json:"failure_reason,omitempty"`
}

type ClaimPayoutIntentsRequest struct {
	ExecutorID       string `json:"executor_id"`
	LeaseTTLSeconds  int    `json:"lease_ttl_seconds"`
	RoundID          string `json:"round_id,omitempty"`
	MemberEthAddress string `json:"member_eth_address,omitempty"`
	Limit            int    `json:"limit,omitempty"`
}

type RenewPayoutIntentsRequest struct {
	ExecutorID      string `json:"executor_id"`
	LeaseID         string `json:"lease_id"`
	LeaseTTLSeconds int    `json:"lease_ttl_seconds"`
}

type ReleasePayoutIntentsRequest struct {
	ExecutorID string   `json:"executor_id"`
	LeaseID    string   `json:"lease_id"`
	IDs        []string `json:"ids,omitempty"`
}

type RequeuePayoutIntentsRequest struct {
	IDs []string `json:"ids"`
}

type PayoutAlert struct {
	Type              string       `json:"type"`
	Severity          string       `json:"severity"`
	Message           string       `json:"message"`
	Intent            PayoutIntent `json:"intent"`
	AgeSeconds        int64        `json:"age_seconds,omitempty"`
	RetryAfterSeconds int64        `json:"retry_after_seconds,omitempty"`
}

type PayoutAlertSummary struct {
	AlertCount             int `json:"alert_count"`
	CriticalCount          int `json:"critical_count"`
	WarningCount           int `json:"warning_count"`
	SubmittedStaleCount    int `json:"submitted_stale_count"`
	FailedStaleCount       int `json:"failed_stale_count"`
	LeaseExpiringSoonCount int `json:"lease_expiring_soon_count"`
	RetryLimitCount        int `json:"retry_limit_count"`
	RecentRequeueCount     int `json:"recent_requeue_count"`
}

func (c *Client) ClaimPayoutIntents(ctx context.Context, reqBody ClaimPayoutIntentsRequest) (string, []PayoutIntent, error) {
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return "", nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.adminURL("/admin/v1/payout-intents/claim", nil), bytes.NewReader(raw))
	if err != nil {
		return "", nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("claim payout intents: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", nil, fmt.Errorf("pool-controller returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		LeaseID string         `json:"lease_id"`
		Intents []PayoutIntent `json:"intents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", nil, fmt.Errorf("decode claim response: %w", err)
	}
	return payload.LeaseID, payload.Intents, nil
}

func (c *Client) RenewPayoutIntents(ctx context.Context, reqBody RenewPayoutIntentsRequest) (string, []PayoutIntent, error) {
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return "", nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.adminURL("/admin/v1/payout-intents/renew", nil), bytes.NewReader(raw))
	if err != nil {
		return "", nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("renew payout intents: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", nil, fmt.Errorf("pool-controller returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		LeaseID string         `json:"lease_id"`
		Intents []PayoutIntent `json:"intents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", nil, fmt.Errorf("decode renew response: %w", err)
	}
	return payload.LeaseID, payload.Intents, nil
}

func (c *Client) ReleasePayoutIntents(ctx context.Context, reqBody ReleasePayoutIntentsRequest) (string, []PayoutIntent, error) {
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return "", nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.adminURL("/admin/v1/payout-intents/release", nil), bytes.NewReader(raw))
	if err != nil {
		return "", nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("release payout intents: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return "", nil, fmt.Errorf("pool-controller returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		LeaseID string         `json:"lease_id"`
		Intents []PayoutIntent `json:"intents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", nil, fmt.Errorf("decode release response: %w", err)
	}
	return payload.LeaseID, payload.Intents, nil
}

func (c *Client) RequeuePayoutIntents(ctx context.Context, reqBody RequeuePayoutIntentsRequest) ([]PayoutIntent, error) {
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.adminURL("/admin/v1/payout-intents/requeue", nil), bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requeue payout intents: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("pool-controller returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Intents []PayoutIntent `json:"intents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode requeue response: %w", err)
	}
	return payload.Intents, nil
}

func NewClient(cfg config.PoolController) (*Client, error) {
	base := strings.TrimRight(cfg.URL, "/")
	u, err := url.Parse(base)
	if err != nil {
		return nil, err
	}
	token, err := resolveBearerToken(cfg)
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

func (c *Client) ListPayoutIntents(ctx context.Context, opts ListPayoutIntentsOptions) ([]PayoutIntent, error) {
	query := url.Values{}
	if opts.RoundID != "" {
		query.Set("round_id", opts.RoundID)
	}
	if opts.MemberEthAddress != "" {
		query.Set("member_eth_address", opts.MemberEthAddress)
	}
	if opts.Status != "" {
		query.Set("status", opts.Status)
	}
	if opts.Limit > 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.adminURL("/admin/v1/payout-intents", query), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list payout intents: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("pool-controller returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Intents []PayoutIntent `json:"intents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode payout intents: %w", err)
	}
	return payload.Intents, nil
}

func (c *Client) ListPayoutAlerts(ctx context.Context, opts ListPayoutAlertsOptions) (PayoutAlertSummary, []PayoutAlert, error) {
	query := url.Values{}
	if opts.RoundID != "" {
		query.Set("round_id", opts.RoundID)
	}
	if opts.MemberEthAddress != "" {
		query.Set("member_eth_address", opts.MemberEthAddress)
	}
	if opts.Status != "" {
		query.Set("status", opts.Status)
	}
	if opts.Limit > 0 {
		query.Set("limit", strconv.Itoa(opts.Limit))
	}
	if opts.SubmittedOlderThanSeconds > 0 {
		query.Set("submitted_older_than_seconds", strconv.Itoa(opts.SubmittedOlderThanSeconds))
	}
	if opts.FailedOlderThanSeconds > 0 {
		query.Set("failed_older_than_seconds", strconv.Itoa(opts.FailedOlderThanSeconds))
	}
	if opts.LeaseExpiresWithinSeconds > 0 {
		query.Set("lease_expires_within_seconds", strconv.Itoa(opts.LeaseExpiresWithinSeconds))
	}
	if opts.RetryCountAtLeast > 0 {
		query.Set("retry_count_at_least", strconv.Itoa(opts.RetryCountAtLeast))
	}
	if opts.RecentRequeueWithinSeconds > 0 {
		query.Set("recent_requeue_within_seconds", strconv.Itoa(opts.RecentRequeueWithinSeconds))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.adminURL("/admin/v1/payout-alerts", query), nil)
	if err != nil {
		return PayoutAlertSummary{}, nil, fmt.Errorf("build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return PayoutAlertSummary{}, nil, fmt.Errorf("list payout alerts: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return PayoutAlertSummary{}, nil, fmt.Errorf("pool-controller returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Summary PayoutAlertSummary `json:"summary"`
		Alerts  []PayoutAlert      `json:"alerts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return PayoutAlertSummary{}, nil, fmt.Errorf("decode payout alerts: %w", err)
	}
	return payload.Summary, payload.Alerts, nil
}

func (c *Client) UpdatePayoutIntentStatus(ctx context.Context, reqBody UpdatePayoutIntentStatusRequest) ([]PayoutIntent, error) {
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.adminURL("/admin/v1/payout-intents/status", nil), bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update payout intent status: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("pool-controller returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var payload struct {
		Intents []PayoutIntent `json:"intents"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode status update response: %w", err)
	}
	return payload.Intents, nil
}

func (c *Client) adminURL(path string, query url.Values) string {
	base, _ := url.Parse(c.baseURL)
	u := base.ResolveReference(&url.URL{Path: path})
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}
	return u.String()
}

func resolveBearerToken(cfg config.PoolController) (string, error) {
	if token := strings.TrimSpace(cfg.BearerToken); token != "" {
		return token, nil
	}
	ref := cfg.BearerTokenRef
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
