// Package errors carries the Go-side LivepeerBrokerError equivalent of
// the TypeScript half's LivepeerBrokerError class.
package errors

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	hdr "github.com/Cloud-SPE/livepeer-network-rewrite/gateway-adapters/go/headers"
)

// BrokerError surfaces a non-2xx response from the broker. Carries the
// structured Livepeer-Error code, optional Livepeer-Backoff advice,
// and the echoed Livepeer-Request-Id when available.
type BrokerError struct {
	Status         int
	Code           string
	Message        string
	BackoffSeconds int
	RequestID      string
	ResponseBody   []byte
}

func (e *BrokerError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message != "" {
		return fmt.Sprintf("livepeer broker error: %s (code=%s, status=%d)", e.Message, e.Code, e.Status)
	}
	return fmt.Sprintf("livepeer broker error: code=%s status=%d", e.Code, e.Status)
}

// FromResponse builds a BrokerError from a non-2xx HTTP response.
// Decodes the body as UTF-8, attempts to parse JSON for a `message`
// field, and reads the standard Livepeer-* response headers.
func FromResponse(status int, headers http.Header, body []byte) *BrokerError {
	code := headers.Get(hdr.Error)
	if code == "" {
		code = "unknown"
	}
	requestID := headers.Get(hdr.RequestID)

	var backoffSeconds int
	if raw := headers.Get(hdr.Backoff); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			backoffSeconds = n
		}
	}

	message := fmt.Sprintf("broker error: %s", code)
	var parsed struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil && parsed.Message != "" {
		message = parsed.Message
	}

	return &BrokerError{
		Status:         status,
		Code:           code,
		Message:        message,
		BackoffSeconds: backoffSeconds,
		RequestID:      requestID,
		ResponseBody:   append([]byte(nil), body...),
	}
}
