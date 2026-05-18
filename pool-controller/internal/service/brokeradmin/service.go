package brokeradmin

import (
	"context"
	"encoding/json"
	"fmt"
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
