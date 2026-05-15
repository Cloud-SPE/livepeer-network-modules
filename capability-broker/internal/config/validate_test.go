package config

import "testing"

func TestValidateDefaultsHTTPHealthProbe(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:              "openai:chat-completions:test",
			OfferingID:      "default",
			InteractionMode: "http-stream@v0",
			WorkUnit: WorkUnit{
				Name:      "tokens",
				Extractor: map[string]any{"type": "openai-usage"},
			},
			Price: Price{AmountWei: "1", PerUnits: 1},
			Backend: Backend{
				Transport: "http",
				URL:       "http://backend:8000/v1/chat/completions",
			},
		}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	cap := cfg.Capabilities[0]
	if got := cap.Health.InitialStatus; got != "stale" {
		t.Fatalf("initial status = %q, want stale", got)
	}
	if got := cap.Health.Probe.Type; got != "http-status" {
		t.Fatalf("probe type = %q, want http-status", got)
	}
	if got := cap.Health.Probe.Config["url"]; got != "http://backend:8000/v1/chat/completions" {
		t.Fatalf("probe url = %v", got)
	}
}
