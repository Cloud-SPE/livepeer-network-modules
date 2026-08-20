package config

import (
	"strings"
	"testing"
)

func TestValidateDefaultsHTTPHealthProbe(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:         "openai:chat-completions",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
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

func TestValidateAllowsWorkerVirtualBackendURL(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:         "openai:chat-completions",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
			WorkUnit: WorkUnit{
				Name:      "tokens",
				Extractor: map[string]any{"type": "openai-usage"},
			},
			Price: Price{AmountWei: "1", PerUnits: 1},
			Backend: Backend{
				Transport: "http",
				ID:        "host-1-gpu-0-chat",
				URL:       "worker://host-1-gpu-0-chat/v1/chat/completions",
			},
			Health: Health{
				Probe: HealthProbe{
					Type:   "http-status",
					Config: map[string]any{"url": "worker://host-1-gpu-0-chat/healthz"},
				},
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
}

func TestValidateAdminAuth(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		AdminAuth: AuthConfig{
			Method:    "bearer",
			SecretRef: "env://BROKER_ADMIN_TOKEN",
		},
		Capabilities: []Capability{{
			ID:         "openai:chat-completions",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
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

	cfg.AdminAuth.SecretRef = "vault://not-supported-here"
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "admin_auth.secret_ref must use env://") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateDefaultsPoolSnapshotPollingConfig(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		PoolSnapshot: PoolSnapshot{
			URL: "http://pool-controller:8080",
		},
		Capabilities: []Capability{{
			ID:         "openai:chat-completions",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
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
	if cfg.PoolSnapshot.TimeoutMS != 1500 {
		t.Fatalf("pool_snapshot.timeout_ms = %d; want 1500", cfg.PoolSnapshot.TimeoutMS)
	}
	if cfg.PoolSnapshot.PollIntervalMS != 5000 {
		t.Fatalf("pool_snapshot.poll_interval_ms = %d; want 5000", cfg.PoolSnapshot.PollIntervalMS)
	}
	if cfg.PoolSnapshot.StaleAfterMS != 15000 {
		t.Fatalf("pool_snapshot.stale_after_ms = %d; want 15000", cfg.PoolSnapshot.StaleAfterMS)
	}
	if cfg.PoolSnapshot.ExpireAfterMS != 60000 {
		t.Fatalf("pool_snapshot.expire_after_ms = %d; want 60000", cfg.PoolSnapshot.ExpireAfterMS)
	}
}

func TestValidateRejectsPoolSnapshotWithoutURL(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		PoolSnapshot: PoolSnapshot{
			TimeoutMS: 1000,
		},
		Capabilities: []Capability{{
			ID:         "openai:chat-completions",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
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

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "pool_snapshot.url is required") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsDeprecatedOpenAICapabilityIDSyntax(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:         "openai:chat-completions:llama-3-70b",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
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
			ID:         "openai:chat-completions",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
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
			ID:         "openai:chat-completions",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
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
			ID:         "openai:chat-completions",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
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
			ID:         "openai:chat-completions",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
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
			ID:         "openai:chat-completions",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
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
			ID:         "openai:chat-completions",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
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

func TestValidateAllowsRepeatedPublishedTupleWithDistinctBackends(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Protocol:   "paid-job/v1",
				Job:        &JobCapability{Transports: []string{"unary"}},
				WorkUnit: WorkUnit{
					Name:      "tokens",
					Extractor: map[string]any{"type": "openai-usage", "field": "total_tokens"},
				},
				Price: Price{AmountWei: "1", PerUnits: 1},
				Backend: Backend{
					ID:        "backend-a",
					Transport: "http",
					URL:       "http://backend-a:8000/v1/chat/completions",
				},
				Extra: map[string]any{
					"openai":   map[string]any{"model": "llama-3-70b"},
					"provider": "vllm",
				},
			},
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Protocol:   "paid-job/v1",
				Job:        &JobCapability{Transports: []string{"unary"}},
				WorkUnit: WorkUnit{
					Name:      "tokens",
					Extractor: map[string]any{"type": "openai-usage", "field": "total_tokens"},
				},
				Price: Price{AmountWei: "1", PerUnits: 1},
				Backend: Backend{
					ID:        "backend-b",
					Transport: "http",
					URL:       "http://backend-b:8000/v1/chat/completions",
				},
				Extra: map[string]any{
					"openai":   map[string]any{"model": "llama-3-70b"},
					"provider": "vllm",
				},
			},
		},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsRepeatedPublishedTupleWithMismatchedPrice(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Protocol:   "paid-job/v1",
				Job:        &JobCapability{Transports: []string{"unary"}},
				WorkUnit: WorkUnit{
					Name:      "tokens",
					Extractor: map[string]any{"type": "openai-usage", "field": "total_tokens"},
				},
				Price: Price{AmountWei: "1", PerUnits: 1},
				Backend: Backend{
					ID:        "backend-a",
					Transport: "http",
					URL:       "http://backend-a:8000/v1/chat/completions",
				},
				Extra: map[string]any{
					"openai":   map[string]any{"model": "llama-3-70b"},
					"provider": "vllm",
				},
			},
			{
				ID:         "openai:chat-completions",
				OfferingID: "shared",
				Protocol:   "paid-job/v1",
				Job:        &JobCapability{Transports: []string{"unary"}},
				WorkUnit: WorkUnit{
					Name:      "tokens",
					Extractor: map[string]any{"type": "openai-usage", "field": "total_tokens"},
				},
				Price: Price{AmountWei: "2", PerUnits: 1},
				Backend: Backend{
					ID:        "backend-b",
					Transport: "http",
					URL:       "http://backend-b:8000/v1/chat/completions",
				},
				Extra: map[string]any{
					"openai":   map[string]any{"model": "llama-3-70b"},
					"provider": "vllm",
				},
			},
		},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "repeated published tuple must reuse the same price") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsReceiptSink(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		ReceiptSink: ReceiptSink{
			URL: "http://pool-controller:8080",
			Auth: AuthConfig{
				Method:    "bearer",
				SecretRef: "env://POOL_CONTROLLER_TOKEN",
			},
			TimeoutMS: 1500,
		},
		Capabilities: []Capability{{
			ID:         "openai:chat-completions",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
			WorkUnit:   WorkUnit{Name: "tokens", Extractor: map[string]any{"type": "openai-usage"}},
			Price:      Price{AmountWei: "1", PerUnits: 1},
			Backend:    Backend{Transport: "http", URL: "http://backend:8000/v1/chat/completions"},
			Extra: map[string]any{
				"openai":   map[string]any{"model": "llama-3-70b"},
				"provider": "vllm",
			},
		}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsReceiptSinkBearerWithoutSecret(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		ReceiptSink: ReceiptSink{
			URL: "http://pool-controller:8080",
			Auth: AuthConfig{
				Method: "bearer",
			},
		},
		Capabilities: []Capability{{
			ID:         "openai:chat-completions",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
			WorkUnit:   WorkUnit{Name: "tokens", Extractor: map[string]any{"type": "openai-usage"}},
			Price:      Price{AmountWei: "1", PerUnits: 1},
			Backend:    Backend{Transport: "http", URL: "http://backend:8000/v1/chat/completions"},
			Extra: map[string]any{
				"openai":   map[string]any{"model": "llama-3-70b"},
				"provider": "vllm",
			},
		}},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "receipt_sink.auth.secret_ref is required") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsAudioCapabilityWithoutAudioExtra(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:         "openai:audio-transcriptions",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
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
			ID:         "openai:audio-speech",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
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
			ID:         "openai:audio-speech",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
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
			ID:         "video:transcode.vod",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
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
			ID:         "video:transcode.abr",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
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
			ID:         "video:transcode.abr",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
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

func TestValidateRejectsVTuberCapabilityWithoutVTuberExtra(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:         "livepeer:vtuber-session",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
			WorkUnit: WorkUnit{
				Name:      "seconds",
				Extractor: map[string]any{"type": "seconds-elapsed"},
			},
			Price: Price{AmountWei: "1", PerUnits: 1},
			Backend: Backend{
				Transport: "http",
				URL:       "http://backend:8080/api/sessions/start",
			},
			Extra: map[string]any{
				"provider": "vtuber-runner",
			},
		}},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "extra.vtuber is required") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsVTuberCapabilityWithWrongTask(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:         "livepeer:vtuber-session",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
			WorkUnit: WorkUnit{
				Name:      "seconds",
				Extractor: map[string]any{"type": "seconds-elapsed"},
			},
			Price: Price{AmountWei: "1", PerUnits: 1},
			Backend: Backend{
				Transport: "http",
				URL:       "http://backend:8080/api/sessions/start",
			},
			Extra: map[string]any{
				"provider": "vtuber-runner",
				"vtuber":   map[string]any{"task": "avatar"},
			},
		}},
	}

	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "extra.vtuber.task") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateAcceptsVTuberExtraShape(t *testing.T) {
	cfg := &Config{
		Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
		Capabilities: []Capability{{
			ID:         "livepeer:vtuber-session",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &JobCapability{Transports: []string{"unary"}},
			WorkUnit: WorkUnit{
				Name:      "seconds",
				Extractor: map[string]any{"type": "seconds-elapsed"},
			},
			Price: Price{AmountWei: "1", PerUnits: 1},
			Backend: Backend{
				Transport: "http",
				URL:       "http://backend:8080/api/sessions/start",
			},
			Extra: map[string]any{
				"provider": "vtuber-runner",
				"vtuber": map[string]any{
					"task": "session",
				},
			},
		}},
	}

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

// TestExtractorIsPaidJobOnly pins that extractors belong to paid-job.
// A paid-session capability has nothing for one to run on — usage is
// runner-reported — and requiring one made operators declare a type
// that is never called.
func TestExtractorIsPaidJobOnly(t *testing.T) {
	base := func() *Config {
		return &Config{
			Identity:        Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
			ExternalBaseURL: "https://broker.example.com",
			SessionStore:    SessionStore{Path: "/tmp/s.db", SealingKeyFile: "/tmp/k"},
			Capabilities: []Capability{{
				ID: "cap:sess", OfferingID: "default", Protocol: "paid-session/v1",
				Session: &SessionCap{
					DescriptorSchema: "sfu-room/v1",
					Runner:           SessionRunnerPaths{CreatePath: "/s", TerminatePath: "/s/{id}"},
				},
				WorkUnit: WorkUnit{Name: "participant_seconds"},
				Price:    Price{AmountWei: "1", PerUnits: 1},
				Backend:  Backend{Transport: "http", URL: "http://runner:8080"},
			}},
		}
	}

	// No extractor: valid.
	if err := base().Validate(); err != nil {
		t.Fatalf("paid-session without an extractor should validate: %v", err)
	}

	// Declaring one is rejected rather than silently ignored — config
	// the broker never reads is config that drifts into a lie.
	withExtractor := base()
	withExtractor.Capabilities[0].WorkUnit.Extractor = map[string]any{"type": "seconds-elapsed"}
	err := withExtractor.Validate()
	if err == nil || !strings.Contains(err.Error(), "not valid for paid-session") {
		t.Fatalf("paid-session with an extractor should be rejected, got %v", err)
	}

	// paid-job still requires one.
	job := base()
	job.Capabilities[0] = Capability{
		ID: "cap:job", OfferingID: "default", Protocol: "paid-job/v1",
		Job:      &JobCapability{Transports: []string{"unary"}},
		WorkUnit: WorkUnit{Name: "tokens"},
		Price:    Price{AmountWei: "1", PerUnits: 1},
		Backend:  Backend{Transport: "http", URL: "http://backend:8080"},
	}
	if err := job.Validate(); err == nil || !strings.Contains(err.Error(), "extractor is required") {
		t.Fatalf("paid-job without an extractor should be rejected, got %v", err)
	}
}

// The v0 media-plane transports were removed with internal/media. They
// must be refused at config time: accepting them let a config load,
// validate, and get advertised, then fail at request time.
func TestValidateRejectsRemovedMediaTransports(t *testing.T) {
	for _, transport := range []string{"ffmpeg-subprocess", "session-runner"} {
		cfg := &Config{
			Identity: Identity{OrchEthAddress: "0x1234567890abcdef1234567890abcdef12345678"},
			Capabilities: []Capability{{
				ID: "test:cap", OfferingID: "default", Protocol: "paid-job/v1",
				Job:      &JobCapability{Transports: []string{"unary"}},
				WorkUnit: WorkUnit{Name: "seconds", Extractor: map[string]any{"type": "seconds-elapsed"}},
				Price:    Price{AmountWei: "1", PerUnits: 1},
				Backend:  Backend{Transport: transport, URL: "http://backend:8080"},
			}},
		}
		err := cfg.Validate()
		if err == nil {
			t.Fatalf("transport %q still validates; nothing implements it", transport)
		}
		if !strings.Contains(err.Error(), "not supported") {
			t.Fatalf("transport %q: unhelpful error %v", transport, err)
		}
	}
}
