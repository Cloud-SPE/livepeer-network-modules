package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

// NewWebhookAlert builds the outbound-only webhook AlertFunc (plan
// 0042 §8). Delivery is best-effort by contract — the audit log is
// the system of record — so failures log and move on; nothing
// retries or blocks the loop. The payload is generic JSON with a
// "text" summary field, which Slack-compatible receivers render
// as-is (Q4 proposal).
func NewWebhookAlert(url string, logger *slog.Logger) AlertFunc {
	if url == "" {
		return nil
	}
	client := &http.Client{Timeout: 5 * time.Second}
	return func(kind string, fields map[string]any) {
		payload, err := json.Marshal(map[string]any{
			"kind":   kind,
			"at":     time.Now().UTC().Format(time.RFC3339),
			"text":   fmt.Sprintf("secure-orch agent: %s %v", kind, fields),
			"fields": fields,
		})
		if err != nil {
			logger.Warn("webhook marshal", "kind", kind, "err", err)
			return
		}
		resp, err := client.Post(url, "application/json", bytes.NewReader(payload))
		if err != nil {
			logger.Warn("webhook post", "kind", kind, "err", err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			logger.Warn("webhook status", "kind", kind, "status", resp.StatusCode)
		}
	}
}
