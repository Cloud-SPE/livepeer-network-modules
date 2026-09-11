package sessionengine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// RunnerClient is the broker's contract with a paid-session backend
// (paid-session/v1 §7.1). Paths are operator configuration — the
// protocol imposes no URL space.
type RunnerClient interface {
	// CreateSession binds a runner session. The runner's response
	// carries the raw runtime descriptor for the engine to validate.
	CreateSession(ctx context.Context, req RunnerCreateRequest) (*RunnerCreateResult, error)
	// QuerySession fetches the runner's view (rebind reconciliation).
	// ErrRunnerSessionGone when the runner no longer knows the session.
	QuerySession(ctx context.Context, runnerSessionID string) (*RunnerStatus, error)
	// TerminateSession is idempotent; terminating an unknown session
	// succeeds.
	TerminateSession(ctx context.Context, runnerSessionID, reason string) error
}

// ErrRunnerSessionGone reports a runner that no longer holds the session.
var ErrRunnerSessionGone = fmt.Errorf("sessionengine: runner session gone")

// RunnerCreateRequest is what the broker sends on session create.
type RunnerCreateRequest struct {
	SessionID     string          `json:"session_id"`
	WorkID        string          `json:"work_id"`
	Capability    string          `json:"capability"`
	Offering      string          `json:"offering"`
	SessionParams json.RawMessage `json:"session_params,omitempty"`
	// Callback coordinates: derived from operator config, never from
	// inbound request headers (paid-session/v1 §7.1).
	CallbackURL   string `json:"callback_url"`
	CallbackToken string `json:"callback_token"`
}

// RunnerCreateResult is the runner's create response.
type RunnerCreateResult struct {
	RunnerSessionID string          `json:"runner_session_id"`
	Runtime         json.RawMessage `json:"runtime"`
}

// RunnerStatus is the runner's view of a session.
type RunnerStatus struct {
	RunnerSessionID string `json:"runner_session_id"`
	State           string `json:"state"`
}

// RunnerPaths is the operator-declared URL surface of a backend.
// {id} is substituted with the escaped runner session id.
type RunnerPaths struct {
	Create    string // e.g. "/sessions"
	Status    string // e.g. "/sessions/{id}"
	Terminate string // e.g. "/sessions/{id}"
}

// HTTPRunnerClient talks to a runner over HTTP with configured paths.
type HTTPRunnerClient struct {
	BaseURL   string
	Paths     RunnerPaths
	AuthToken string // operator-configured bearer for broker->runner calls
	Client    *http.Client
}

func (c *HTTPRunnerClient) httpClient() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *HTTPRunnerClient) urlFor(path, runnerSessionID string) string {
	p := strings.ReplaceAll(path, "{id}", url.PathEscape(runnerSessionID))
	return strings.TrimRight(c.BaseURL, "/") + p
}

func (c *HTTPRunnerClient) do(ctx context.Context, method, u string, body any, out any) (int, error) {
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rdr)
	if err != nil {
		return 0, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AuthToken)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if out != nil && resp.StatusCode/100 == 2 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp.StatusCode, fmt.Errorf("sessionengine: decode runner response: %w", err)
		}
	}
	return resp.StatusCode, nil
}

func (c *HTTPRunnerClient) CreateSession(ctx context.Context, req RunnerCreateRequest) (*RunnerCreateResult, error) {
	var out RunnerCreateResult
	code, err := c.do(ctx, http.MethodPost, c.urlFor(c.Paths.Create, ""), req, &out)
	if err != nil {
		return nil, err
	}
	if code/100 != 2 {
		return nil, fmt.Errorf("sessionengine: runner create returned %d", code)
	}
	if out.RunnerSessionID == "" {
		return nil, fmt.Errorf("sessionengine: runner create response missing runner_session_id")
	}
	return &out, nil
}

func (c *HTTPRunnerClient) QuerySession(ctx context.Context, id string) (*RunnerStatus, error) {
	var out RunnerStatus
	code, err := c.do(ctx, http.MethodGet, c.urlFor(c.Paths.Status, id), nil, &out)
	if err != nil {
		return nil, err
	}
	if code == http.StatusNotFound || code == http.StatusGone {
		return nil, ErrRunnerSessionGone
	}
	if code/100 != 2 {
		return nil, fmt.Errorf("sessionengine: runner status returned %d", code)
	}
	return &out, nil
}

func (c *HTTPRunnerClient) TerminateSession(ctx context.Context, id, reason string) error {
	body := map[string]string{"reason": reason}
	code, err := c.do(ctx, http.MethodDelete, c.urlFor(c.Paths.Terminate, id), body, nil)
	if err != nil {
		return err
	}
	// Idempotent: unknown session is success.
	if code/100 == 2 || code == http.StatusNotFound || code == http.StatusGone {
		return nil
	}
	return fmt.Errorf("sessionengine: runner terminate returned %d", code)
}
