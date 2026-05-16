package registry

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/health"
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
		Capabilities: []config.Capability{{
			ID:         "openai:chat-completions",
			OfferingID: "default",
			Health: config.Health{
				InitialStatus: "ready",
			},
		}},
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
