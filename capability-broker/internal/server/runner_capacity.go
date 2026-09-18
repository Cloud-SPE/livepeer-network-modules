package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
)

const (
	defaultCapacityBackoffSeconds = 5
	maxCapacityBackoffSeconds     = 60
)

// runnerCapacityRefusal recognizes the deliberately narrow private-runner
// contract. A generic 429 is not enough: applications are allowed to use that
// status for their own API limits, and the broker must not reinterpret those
// responses as host admission.
func runnerCapacityRefusal(status int, header http.Header, body []byte) (int, bool) {
	if status != http.StatusTooManyRequests {
		return 0, false
	}
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Error != "capacity_reached" {
		return 0, false
	}
	backoff := defaultCapacityBackoffSeconds
	if raw := strings.TrimSpace(header.Get("Retry-After")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			if n > maxCapacityBackoffSeconds {
				n = maxCapacityBackoffSeconds
			}
			backoff = n
		}
	}
	return backoff, true
}

func writeCapacityRefusal(w http.ResponseWriter, backoff int) {
	if backoff <= 0 || backoff > maxCapacityBackoffSeconds {
		backoff = defaultCapacityBackoffSeconds
	}
	w.Header().Set(livepeerheader.Backoff, strconv.Itoa(backoff))
	w.Header().Set(livepeerheader.WorkUnits, "0")
	livepeerheader.WriteError(w, http.StatusServiceUnavailable,
		livepeerheader.ErrCapacityExhausted, "selected runner has no compatible host capacity")
}
