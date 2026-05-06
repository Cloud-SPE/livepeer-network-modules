// Package brokerclient is the boundary HTTP adapter for capability-
// broker /registry/offerings scrapes.
//
// Two failure tiers, per plan 0018 §5:
//
//   - Soft failure: broker unreachable, 5xx, timeout. ErrBrokerUnreachable.
//     Caller keeps last-good entries marked stale_failing.
//   - Hard failure: malformed JSON or schema-invalid payload.
//     ErrBrokerSchema. Caller drops broker entries from the candidate
//     immediately.
//
// HTTPS is allowed but not required on the LAN; the broker advertises
// itself over plain HTTP per default.
package brokerclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/types"
)

// Errors. Caller switches on these via errors.Is.
var (
	ErrBrokerUnreachable = errors.New("brokerclient: unreachable or transient HTTP failure")
	ErrBrokerSchema      = errors.New("brokerclient: schema-invalid response")
)

// Client is the brokerclient interface. Real impl is HTTPClient; tests
// inject a fake.
type Client interface {
	Fetch(ctx context.Context, baseURL string) (*types.BrokerOfferings, error)
}

// HTTPClient is the production brokerclient.
type HTTPClient struct {
	HTTP    *http.Client
	Timeout time.Duration
}

// New returns an HTTPClient with the configured per-request timeout.
func New(timeout time.Duration) *HTTPClient {
	return &HTTPClient{
		HTTP:    &http.Client{Timeout: timeout},
		Timeout: timeout,
	}
}

// Fetch issues `GET <baseURL>/registry/offerings` and decodes the
// response. Caller is responsible for the per-broker context (deadline,
// cancellation).
func (c *HTTPClient) Fetch(ctx context.Context, baseURL string) (*types.BrokerOfferings, error) {
	url := strings.TrimRight(baseURL, "/") + "/registry/offerings"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrBrokerUnreachable, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBrokerUnreachable, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %v", ErrBrokerUnreachable, err)
	}

	if resp.StatusCode >= 500 {
		return nil, fmt.Errorf("%w: HTTP %d", ErrBrokerUnreachable, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP %d", ErrBrokerSchema, resp.StatusCode)
	}

	var out types.BrokerOfferings
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ErrBrokerSchema, err)
	}
	return &out, nil
}
