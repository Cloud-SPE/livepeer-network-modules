package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
)

type stubMetadataStatusSource struct {
	status map[string]MetadataStatus
}

func (s stubMetadataStatusSource) StatusFor(capabilityID, offeringID string) (MetadataStatus, bool) {
	st, ok := s.status[capabilityID+"|"+offeringID]
	return st, ok
}

func TestHealthHandler_EmbedsMetadataStatus(t *testing.T) {
	mgr := health.New(&config.Config{
		Capabilities: []config.Capability{
			{
				ID:         "openai:chat-completions",
				OfferingID: "default",
				Backend:    config.Backend{ID: "backend-a", URL: "http://backend-a"},
				Health: config.Health{
					InitialStatus: "ready",
				},
			},
			{
				ID:         "openai:chat-completions",
				OfferingID: "default",
				Backend:    config.Backend{ID: "backend-b", URL: "http://backend-b"},
				Health: config.Health{
					InitialStatus: "draining",
					Drain:         config.HealthDrain{Enabled: true},
					Probe:         config.HealthProbe{Type: "manual-drain"},
				},
			},
		},
	})
	meta := stubMetadataStatusSource{
		status: map[string]MetadataStatus{
			"openai:chat-completions|default": {
				Provider:            "vllm",
				Applicable:          true,
				LastAttemptAt:       time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC),
				LastSuccessAt:       time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC),
				LastResult:          "enriched",
				ConsecutiveFailures: 2,
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/registry/health", nil)
	rec := httptest.NewRecorder()
	HealthHandler(mgr, meta).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want %d", rec.Code, http.StatusOK)
	}

	var out struct {
		Capabilities []struct {
			ID       string `json:"id"`
			Status   string `json:"status"`
			Backends []struct {
				BackendID         string `json:"backend_id"`
				Status            string `json:"status"`
				SelectionEligible bool   `json:"selection_eligible"`
				SelectionWeight   int    `json:"selection_weight"`
				SelectionReason   string `json:"selection_reason"`
			} `json:"backends"`
			Metadata struct {
				Provider              string  `json:"provider"`
				Applicable            bool    `json:"applicable"`
				LastResult            string  `json:"last_result"`
				LastSuccessAgeSeconds float64 `json:"last_success_age_seconds"`
				ConsecutiveFailures   int     `json:"consecutive_failures"`
			} `json:"metadata"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(out.Capabilities) != 1 {
		t.Fatalf("capabilities count = %d; want 1", len(out.Capabilities))
	}
	got := out.Capabilities[0]
	if got.Status != "ready" {
		t.Fatalf("capability status = %q; want ready", got.Status)
	}
	if len(got.Backends) != 2 {
		t.Fatalf("backend count = %d; want 2", len(got.Backends))
	}
	if !got.Backends[0].SelectionEligible || got.Backends[0].SelectionWeight == 0 {
		t.Fatalf("backend[0] selection fields = %+v; want eligible positive weight", got.Backends[0])
	}
	if got.Backends[0].SelectionReason == "" {
		t.Fatalf("backend[0] selection reason empty: %+v", got.Backends[0])
	}
	if got.Backends[1].SelectionEligible || got.Backends[1].SelectionWeight != 0 {
		t.Fatalf("backend[1] selection fields = %+v; want ineligible zero weight", got.Backends[1])
	}
	if got.Metadata.Provider != "vllm" {
		t.Fatalf("metadata.provider = %q; want vllm", got.Metadata.Provider)
	}
	if !got.Metadata.Applicable {
		t.Fatal("metadata.applicable = false; want true")
	}
	if got.Metadata.LastResult != "enriched" {
		t.Fatalf("metadata.last_result = %q; want enriched", got.Metadata.LastResult)
	}
	if got.Metadata.ConsecutiveFailures != 2 {
		t.Fatalf("metadata.consecutive_failures = %d; want 2", got.Metadata.ConsecutiveFailures)
	}
	if got.Metadata.LastSuccessAgeSeconds < 0 {
		t.Fatalf("metadata.last_success_age_seconds = %v; want non-negative", got.Metadata.LastSuccessAgeSeconds)
	}
}
