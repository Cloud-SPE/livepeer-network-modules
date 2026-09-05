// Package desiredstate keeps this host running what the pool asked for.
//
// The agent holds no policy. It asks the controller what should be
// running here, makes the host match, and reports what happened. Every
// decision — which template, which GPU, when to withdraw one — was made
// upstream, so an agent that disagrees with the controller is a bug in
// the agent.
package desiredstate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Document mirrors the controller's desired-state response.
type Document struct {
	EnrollmentID string    `json:"enrollment_id"`
	Revision     string    `json:"revision"`
	Services     []Service `json:"services"`
}

type Service struct {
	Name            string   `json:"name"`
	ComposeFragment string   `json:"compose_fragment"`
	DeviceIDs       []string `json:"device_ids"`
	RTMPPort        int      `json:"rtmp_port,omitempty"`
	Models          []Model  `json:"models,omitempty"`
	Draining        bool     `json:"draining,omitempty"`
	TemplateID      string   `json:"template_id"`
	AssignmentID    string   `json:"assignment_id"`
	// Capability, Protocol and Identity are what this runner must
	// declare at attach. The controller supplies them because it is the
	// only side that knows what the offer selects on.
	Capability string            `json:"capability"`
	Protocol   string            `json:"protocol,omitempty"`
	Identity   map[string]string `json:"identity,omitempty"`
}

type Model struct {
	Name      string `json:"name"`
	SizeBytes uint64 `json:"size_bytes,omitempty"`
	Source    string `json:"source,omitempty"`
}

// StatusReport is what the agent sends back.
type StatusReport struct {
	Revision string          `json:"revision"`
	Services []ServiceStatus `json:"services"`
}

type ServiceStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Service status values. These are the agent's honest report of what
// the host achieved, not what it was asked to do.
const (
	StatusRunning = "running"
	StatusStopped = "stopped"
	StatusFailed  = "failed"
)

// Client talks to the controller's member API.
type Client struct {
	BaseURL      string
	EnrollmentID string
	Token        string
	HTTP         *http.Client

	// etag carries the last revision seen, so an unchanged pool costs
	// one conditional request and no body.
	etag string
}

func New(baseURL, enrollmentID, token string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		BaseURL:      strings.TrimRight(baseURL, "/"),
		EnrollmentID: enrollmentID,
		Token:        token,
		HTTP:         &http.Client{Timeout: timeout},
	}
}

// ErrUnchanged says the pool wants exactly what it wanted last time.
var ErrUnchanged = fmt.Errorf("desired state unchanged")

// Fetch returns the desired state, or ErrUnchanged.
func (c *Client) Fetch(ctx context.Context) (Document, error) {
	url := fmt.Sprintf("%s/member/v1/enrollments/%s/desired-state", c.BaseURL, c.EnrollmentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Document{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if c.etag != "" {
		req.Header.Set("If-None-Match", `"`+c.etag+`"`)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Document{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotModified {
		return Document{}, ErrUnchanged
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return Document{}, fmt.Errorf("desired-state: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var doc Document
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return Document{}, err
	}
	c.etag = doc.Revision
	return doc, nil
}

// Report sends what the host managed to do.
func (c *Client) Report(ctx context.Context, report StatusReport) error {
	url := fmt.Sprintf("%s/member/v1/enrollments/%s/status", c.BaseURL, c.EnrollmentID)
	raw, err := json.Marshal(report)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(raw)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("status report: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}
