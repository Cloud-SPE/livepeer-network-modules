package desiredstate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Credential rotation (plan 0044 §5 D7).
//
// The agent rotates its own enrollment token rather than waiting for a
// member to notice it expired. A host that loses its credential stops
// earning and its owner finds out from a dashboard, which is the worst
// possible way to learn it — so the agent asks for a fresh one while
// the old one still works.

// RotateResponse is what the controller returns.
type RotateResponse struct {
	Enrollment struct {
		ID                      string `json:"id"`
		BrokerSessionCredential string `json:"broker_session_credential,omitempty"`
	} `json:"enrollment"`
	Token string `json:"enrollment_token"`
}

// Rotate asks for a new enrollment token and writes it where the agent
// reads it from.
//
// The write is the delicate part: the new token is returned exactly
// once, so losing it between the response and the disk means this host
// can no longer authenticate at all. It is written to a temp file and
// renamed, and the OLD token is left in place until the new one is
// safely down.
func (c *Client) Rotate(ctx context.Context, tokenPath string) (string, error) {
	url := fmt.Sprintf("%s/member/v1/enrollments/%s/rotate", c.BaseURL, c.EnrollmentID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("rotate: %d %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var out RotateResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.Token) == "" {
		return "", fmt.Errorf("rotate: controller returned no token")
	}
	if strings.TrimSpace(tokenPath) != "" {
		if err := writeSecret(tokenPath, out.Token); err != nil {
			// The rotation already happened server-side: the old token
			// is dead and the new one is only in memory. Say so loudly
			// rather than returning as though nothing was lost.
			return out.Token, fmt.Errorf("rotated but could not persist the new token to %s: %w", tokenPath, err)
		}
	}
	c.Token = out.Token
	return out.Token, nil
}

func writeSecret(path, value string) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(value+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
