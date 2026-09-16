// Package ownership defines the optional Go client for the shared managed-GPU
// ownership authority. It does not attest physical device identity.
package ownership

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
)

type Record struct {
	DeviceID          string `json:"device_id"`
	PoolID            string `json:"pool_id"`
	EnrollmentID      string `json:"enrollment_id"`
	MemberWallet      string `json:"member_wallet,omitempty"`
	Generation        uint64 `json:"generation"`
	State             string `json:"state"`
	DestinationPoolID string `json:"destination_pool_id,omitempty"`
}

type Request struct {
	DeviceID           string `json:"device_id"`
	PoolID             string `json:"pool_id"`
	EnrollmentID       string `json:"enrollment_id"`
	MemberWallet       string `json:"member_wallet,omitempty"`
	ExpectedGeneration uint64 `json:"expected_generation"`
	DestinationPoolID  string `json:"destination_pool_id,omitempty"`
	DrainEvidence      string `json:"drain_evidence,omitempty"`
	RevocationEvidence string `json:"revocation_evidence,omitempty"`
	StopEvidence       string `json:"stop_evidence,omitempty"`
	Reason             string `json:"reason,omitempty"`
}

type Config struct {
	URL       string `yaml:"url,omitempty" json:"url,omitempty"`
	PoolID    string `yaml:"pool_id,omitempty" json:"pool_id,omitempty"`
	TokenFile string `yaml:"token_file,omitempty" json:"token_file,omitempty"`
	CAFile    string `yaml:"ca_file,omitempty" json:"ca_file,omitempty"`
}

type Client struct {
	base, poolID string
	http         *http.Client
}

func NewClient(cfg Config) (*Client, error) {
	client, err := serviceauth.HTTPSClientWithCAFile(cfg.URL, cfg.PoolID, cfg.TokenFile, cfg.CAFile)
	if err != nil {
		return nil, err
	}
	return &Client{base: strings.TrimRight(cfg.URL, "/"), poolID: cfg.PoolID, http: client}, nil
}
func (c *Client) Get(ctx context.Context, device string) (Record, error) {
	return c.do(ctx, "GET", "/ownership/v1/devices/"+url.PathEscape(device), nil)
}
func (c *Client) Claim(ctx context.Context, req Request) (Record, error) {
	return c.mutate(ctx, "claim", req)
}
func (c *Client) Drain(ctx context.Context, req Request) (Record, error) {
	return c.mutate(ctx, "drain", req)
}
func (c *Client) Release(ctx context.Context, req Request) (Record, error) {
	return c.mutate(ctx, "release", req)
}
func (c *Client) FencedRecovery(ctx context.Context, req Request) (Record, error) {
	return c.mutate(ctx, "fenced-recovery", req)
}
func (c *Client) mutate(ctx context.Context, action string, req Request) (Record, error) {
	req.PoolID = c.poolID
	return c.do(ctx, "POST", "/ownership/v1/"+action, req)
}
func (c *Client) do(ctx context.Context, method, path string, body any) (Record, error) {
	var out Record
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return out, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return out, fmt.Errorf("ownership unavailable: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return out, fmt.Errorf("ownership request rejected (%d)", res.StatusCode)
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&out)
	return out, err
}
