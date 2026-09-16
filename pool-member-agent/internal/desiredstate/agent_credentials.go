package desiredstate

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
)

type AgentCredentials struct {
	PoolID           string `json:"pool_id"`
	EnrollmentID     string `json:"enrollment_id"`
	Token            string `json:"enrollment_token"`
	AttachCredential string `json:"attach_credential"`
	Generation       uint64 `json:"credential_generation"`
	RequestID        string `json:"rotation_request_id,omitempty"`
	PendingAck       bool   `json:"pending_ack,omitempty"`
}

var requestProofPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (c AgentCredentials) validate() error {
	if c.PoolID == "" || c.EnrollmentID == "" || c.Token == "" || c.AttachCredential == "" || len(c.Token) > 8192 || len(c.AttachCredential) > 8192 || c.RequestID != "" && !requestProofPattern.MatchString(c.RequestID) || c.PendingAck && c.RequestID == "" {
		return fmt.Errorf("invalid durable agent credentials")
	}
	return nil
}
func LoadAgentCredentials(path string) (AgentCredentials, error) {
	var out AgentCredentials
	raw, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}
	if len(raw) > 32768 {
		return out, fmt.Errorf("agent credential file too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return out, fmt.Errorf("invalid agent credential file")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return out, fmt.Errorf("invalid agent credential file")
	}
	return out, out.validate()
}
func SaveAgentCredentials(path string, value AgentCredentials) error {
	if err := value.validate(); err != nil {
		return err
	}
	if path == "" {
		return fmt.Errorf("agent credential path required")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".agent-credentials-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	defer temp.Close()
	if err := json.NewEncoder(temp).Encode(value); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp.Name(), path); err != nil {
		return err
	}
	parent, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}
func (c *Client) credentialRequest(ctx context.Context, method, action string, input any, out any) error {
	var body io.Reader
	if input != nil {
		raw, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/member/v1/enrollments/"+c.EnrollmentID+"/"+action, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	response, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("agent credential request returned %d", response.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 32768)).Decode(out); err != nil {
		return fmt.Errorf("invalid agent credential response")
	}
	return nil
}

// CurrentAgentCredentials is agent-only. It also upgrades older bundles whose
// enrollment token was rotated but whose inline attach credential stayed stale.
func (c *Client) CurrentAgentCredentials(ctx context.Context) (AgentCredentials, error) {
	var out AgentCredentials
	if err := c.credentialRequest(ctx, "GET", "agent-credentials", nil, &out); err != nil {
		return out, err
	}
	out.Token = c.Token
	if out.EnrollmentID != c.EnrollmentID {
		return out, fmt.Errorf("agent credential enrollment mismatch")
	}
	return out, out.validate()
}

// RecoverAgentRotation resumes a persisted request or acknowledgement. With no
// request it only loads credentials; it never starts a rotation by itself.
func (c *Client) RecoverAgentRotation(ctx context.Context, path string) (AgentCredentials, error) {
	state, err := LoadAgentCredentials(path)
	if err != nil {
		return state, err
	}
	if state.EnrollmentID != c.EnrollmentID {
		return state, fmt.Errorf("credential file belongs to another enrollment")
	}
	c.Token = state.Token
	if state.RequestID == "" {
		return state, nil
	}
	request := map[string]string{"request_id": state.RequestID}
	if !state.PendingAck {
		var next AgentCredentials
		if err := c.credentialRequest(ctx, "POST", "agent-rotation", request, &next); err != nil {
			return state, err
		}
		if next.PoolID != state.PoolID || next.EnrollmentID != state.EnrollmentID || next.Generation != state.Generation+1 {
			return state, fmt.Errorf("rotation identity or generation mismatch")
		}
		next.RequestID = state.RequestID
		next.PendingAck = true
		if err := SaveAgentCredentials(path, next); err != nil {
			return state, err
		}
		state = next
		c.Token = state.Token
	}
	if err := c.credentialRequest(ctx, "POST", "agent-rotation-ack", request, nil); err != nil {
		return state, err
	}
	state.RequestID = ""
	state.PendingAck = false
	if err := SaveAgentCredentials(path, state); err != nil {
		return state, err
	}
	return state, nil
}
func (c *Client) RotateAgentCredentials(ctx context.Context, path string) (AgentCredentials, error) {
	state, err := LoadAgentCredentials(path)
	if err != nil {
		return state, err
	}
	if state.EnrollmentID != c.EnrollmentID {
		return state, fmt.Errorf("credential file belongs to another enrollment")
	}
	if state.RequestID == "" {
		var proof [32]byte
		if _, err := rand.Read(proof[:]); err != nil {
			return state, err
		}
		state.RequestID = hex.EncodeToString(proof[:])
		if err := SaveAgentCredentials(path, state); err != nil {
			return state, err
		}
	}
	return c.RecoverAgentRotation(ctx, path)
}
