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
				"openai":   map[string]any{"model": "llama-3-70b"},
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
				"openai":   map[string]any{},
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
				"openai":   "llama-3-70b",
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
				"openai":   map[string]any{"model": "llama-3-70b"},
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
				"openai":            map[string]any{"model": "llama-3-70b"},
				"provider":          "vllm",
				"served_model_name": "Llama 3 70B",
				"backend_model":     "meta-llama/Llama-3-70b",
				"features": map[string]any{
					"streaming": true,
					"tools":     true,
					"json_mode": true,
				},
			},
		}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsAudioCapabilityWithoutAudioExtra(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:              "openai:audio-transcriptions",
			OfferingID:      "default",
			InteractionMode: "http-multipart@v0",
			WorkUnit: WorkUnit{
				Name:      "seconds",
				Extractor: map[string]any{"type": "response-header"},
			},
			Price: Price{AmountWei: "1", PerUnits: 1},
			Backend: Backend{
				Transport: "http",
				URL:       "http://backend:8080/v1/audio/transcriptions",
			},
			Extra: map[string]any{
				"openai":   map[string]any{"model": "whisper-large-v3"},
				"provider": "openai-audio-runner",
			},
		}},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "extra.audio is required") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsAudioCapabilityWithWrongTask(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:              "openai:audio-speech",
			OfferingID:      "default",
			InteractionMode: "http-reqresp@v0",
			WorkUnit: WorkUnit{
				Name:      "characters",
				Extractor: map[string]any{"type": "request-formula"},
			},
			Price: Price{AmountWei: "1", PerUnits: 1},
			Backend: Backend{
				Transport: "http",
				URL:       "http://backend:8080/v1/audio/speech",
			},
			Extra: map[string]any{
				"openai":   map[string]any{"model": "kokoro"},
				"provider": "openai-tts-runner",
				"audio":    map[string]any{"task": "transcription"},
			},
		}},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "extra.audio.task") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsAudioExtraShape(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:              "openai:audio-speech",
			OfferingID:      "default",
			InteractionMode: "http-reqresp@v0",
			WorkUnit: WorkUnit{
				Name:      "characters",
				Extractor: map[string]any{"type": "request-formula"},
			},
			Price: Price{AmountWei: "1", PerUnits: 1},
			Backend: Backend{
				Transport: "http",
				URL:       "http://backend:8080/v1/audio/speech",
			},
			Extra: map[string]any{
				"openai":   map[string]any{"model": "kokoro"},
				"provider": "openai-tts-runner",
				"audio": map[string]any{
					"task": "speech",
				},
			},
		}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsVideoCapabilityWithoutVideoExtra(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:              "video:transcode.vod",
			OfferingID:      "default",
			InteractionMode: "http-reqresp@v0",
			WorkUnit: WorkUnit{
				Name:      "jobs",
				Extractor: map[string]any{"type": "request-formula"},
			},
			Price: Price{AmountWei: "1", PerUnits: 1},
			Backend: Backend{
				Transport: "http",
				URL:       "http://backend:8080/v1/video/transcode",
			},
			Extra: map[string]any{
				"provider": "transcode-runner",
			},
		}},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "extra.video is required") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsVideoCapabilityWithWrongTask(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:              "video:transcode.abr",
			OfferingID:      "default",
			InteractionMode: "http-reqresp@v0",
			WorkUnit: WorkUnit{
				Name:      "jobs",
				Extractor: map[string]any{"type": "request-formula"},
			},
			Price: Price{AmountWei: "1", PerUnits: 1},
			Backend: Backend{
				Transport: "http",
				URL:       "http://backend:8080/v1/video/transcode/abr",
			},
			Extra: map[string]any{
				"provider": "abr-runner",
				"video":    map[string]any{"task": "transcode"},
			},
		}},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "extra.video.task") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsVideoExtraShape(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:              "video:transcode.abr",
			OfferingID:      "default",
			InteractionMode: "http-reqresp@v0",
			WorkUnit: WorkUnit{
				Name:      "jobs",
				Extractor: map[string]any{"type": "request-formula"},
			},
			Price: Price{AmountWei: "1", PerUnits: 1},
			Backend: Backend{
				Transport: "http",
				URL:       "http://backend:8080/v1/video/transcode/abr",
			},
			Extra: map[string]any{
				"provider": "abr-runner",
				"video": map[string]any{
					"task": "abr-transcode",
				},
			},
		}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
