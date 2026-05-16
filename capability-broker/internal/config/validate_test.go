package config

import (
	"strings"
	"testing"
)

func TestValidateDefaultsHTTPHealthProbe(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:              "openai:chat-completions",
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
			Extra: map[string]any{
				"openai": map[string]any{"model": "llama-3-70b"},
				"provider": "vllm",
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

func TestValidateRejectsDeprecatedOpenAICapabilityIDSyntax(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:              "openai:chat-completions:llama-3-70b",
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
			Extra: map[string]any{
				"openai": map[string]any{"model": "llama-3-70b"},
			},
		}},
	}

	err := cfg.Validate()
	if err == nil {
		t.Fatal("Validate() error = nil, want deprecated capability syntax rejection")
	}
	if !strings.Contains(err.Error(), "deprecated OpenAI capability syntax") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsOpenAICapabilityWithoutOpenAIExtra(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:              "openai:chat-completions",
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
			Extra: map[string]any{
				"provider": "vllm",
			},
		}},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "extra.openai is required") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsOpenAICapabilityWithoutModel(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:              "openai:chat-completions",
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
			Extra: map[string]any{
				"openai": map[string]any{},
				"provider": "vllm",
			},
		}},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "extra.openai.model is required") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsOpenAICapabilityWithoutProvider(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:              "openai:chat-completions",
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
			Extra: map[string]any{
				"openai": map[string]any{"model": "llama-3-70b"},
			},
		}},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "extra.provider is required") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsNonMapOpenAIExtra(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:              "openai:chat-completions",
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
			Extra: map[string]any{
				"openai": "llama-3-70b",
				"provider": "vllm",
			},
		}},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "extra.openai must be a map") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsNonBooleanFeatureFlags(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:              "openai:chat-completions",
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
			Extra: map[string]any{
				"openai": map[string]any{"model": "llama-3-70b"},
				"provider": "vllm",
				"features": map[string]any{
					"streaming": "true",
				},
			},
		}},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "extra.features.streaming must be a boolean") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsOpenAIExtraShape(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:              "openai:chat-completions",
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
			Extra: map[string]any{
				"openai": map[string]any{"model": "llama-3-70b"},
				"provider": "vllm",
				"served_model_name": "Llama 3 70B",
				"backend_model": "meta-llama/Llama-3-70b",
				"features": map[string]any{
					"streaming": true,
					"tools": true,
					"json_mode": true,
				},
			},
		}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
