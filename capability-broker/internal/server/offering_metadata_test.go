package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/server/registry"
)

func TestHydrateRunnerMetadata_PopulatesKokoroVoices(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != kokoroOptionsPath {
			t.Fatalf("path = %s; want %s", r.URL.Path, kokoroOptionsPath)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models":        []string{"kokoro"},
			"default_voice": "af_bella",
			"voices": map[string]any{
				"native": []string{"af_bella", "am_michael"},
				"aliases": map[string]string{
					"alloy": "af_bella",
					"echo":  "am_michael",
				},
			},
		})
	}))
	defer ts.Close()

	cfg := &config.Config{
		Capabilities: []config.Capability{
			{
				ID:              "openai:audio-speech",
				OfferingID:      "kokoro",
				InteractionMode: "http-reqresp@v0",
				Backend: config.Backend{
					Transport: "http",
					URL:       ts.URL + "/v1/audio/speech",
				},
				Extra: map[string]any{
					"provider": "openai-tts-runner",
				},
			},
		},
	}

	hydrateRunnerMetadataWithClient(context.Background(), ts.Client(), cfg)

	voices, ok := cfg.Capabilities[0].Extra["voices"].(map[string]any)
	if !ok {
		t.Fatalf("extra.voices missing or wrong type: %#v", cfg.Capabilities[0].Extra["voices"])
	}
	if got := voices["default"]; got != "af_bella" {
		t.Fatalf("default voice = %#v; want af_bella", got)
	}
	native, ok := voices["native"].([]string)
	if !ok {
		t.Fatalf("native voices wrong type: %#v", voices["native"])
	}
	if len(native) != 2 || native[0] != "af_bella" || native[1] != "am_michael" {
		t.Fatalf("native voices = %#v", native)
	}
	aliases, ok := voices["aliases"].(map[string]string)
	if !ok {
		t.Fatalf("aliases wrong type: %#v", voices["aliases"])
	}
	if aliases["alloy"] != "af_bella" || aliases["echo"] != "am_michael" {
		t.Fatalf("aliases = %#v", aliases)
	}
	if cfg.Capabilities[0].Extra["provider"] != "openai-tts-runner" {
		t.Fatalf("provider should be preserved")
	}
}

func TestHydrateRunnerMetadata_SkipsOnFetchFailure(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Capabilities: []config.Capability{
			{
				ID:              "openai:audio-speech",
				OfferingID:      "kokoro",
				InteractionMode: "http-reqresp@v0",
				Backend: config.Backend{
					Transport: "http",
					URL:       "http://127.0.0.1:1/v1/audio/speech",
				},
				Extra: map[string]any{
					"provider": "openai-tts-runner",
				},
			},
		},
	}

	hydrateRunnerMetadata(context.Background(), cfg)

	if _, ok := cfg.Capabilities[0].Extra["voices"]; ok {
		t.Fatalf("voices should not be set on fetch failure")
	}
	if cfg.Capabilities[0].Extra["provider"] != "openai-tts-runner" {
		t.Fatalf("provider should be preserved")
	}
}

func TestDeriveOptionsURL_IgnoresBackendPath(t *testing.T) {
	t.Parallel()

	got, err := deriveOptionsURL("http://runner:8080/v1/audio/speech", kokoroOptionsPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "http://runner:8080/openai-audio-speech/options"
	if got != want {
		t.Fatalf("options URL = %s; want %s", got, want)
	}
}

func TestHydrateRunnerMetadata_PopulatesOpenAIMetadataForVLLM(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %s; want /v1/models", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":   "Qwen3.6-27B",
					"root": "sakamakismile/Qwen3.6-27B-Text-NVFP4-MTP",
				},
			},
		})
	}))
	defer ts.Close()

	cfg := &config.Config{
		Capabilities: []config.Capability{
			{
				ID:              "openai:chat-completions",
				OfferingID:      "default",
				InteractionMode: "http-stream@v0",
				Backend: config.Backend{
					Transport: "http",
					URL:       ts.URL + "/v1/chat/completions",
				},
				Extra: map[string]any{
					"openai":   map[string]any{"model": "Qwen3.6-27B"},
					"provider": "vllm",
				},
			},
		},
	}

	catalog := newMetadataCatalog()
	refreshMetadataCatalog(context.Background(), ts.Client(), cfg, catalog)

	extra := catalog.ExtraFor("openai:chat-completions", "default")
	if got := extra["served_model_name"]; got != "Qwen3.6-27B" {
		t.Fatalf("served_model_name = %#v; want Qwen3.6-27B", got)
	}
	if got := extra["backend_model"]; got != "sakamakismile/Qwen3.6-27B-Text-NVFP4-MTP" {
		t.Fatalf("backend_model = %#v", got)
	}
	features, ok := extra["features"].(map[string]any)
	if !ok {
		t.Fatalf("features missing or wrong type: %#v", extra["features"])
	}
	if got := features["streaming"]; got != true {
		t.Fatalf("features.streaming = %#v; want true", got)
	}
	status, ok := catalog.StatusFor("openai:chat-completions", "default")
	if !ok {
		t.Fatal("expected metadata refresh status")
	}
	if status.Provider != "vllm" {
		t.Fatalf("provider = %q; want vllm", status.Provider)
	}
	if status.LastResult != "enriched" {
		t.Fatalf("last_result = %q; want enriched", status.LastResult)
	}
	if status.LastSuccessAt.IsZero() {
		t.Fatal("last_success_at should be populated")
	}
}

func TestHydrateRunnerMetadata_PreservesOperatorOpenAIValues(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id":   "Qwen3.6-27B",
					"root": "backend/model",
				},
			},
		})
	}))
	defer ts.Close()

	cfg := &config.Config{
		Capabilities: []config.Capability{
			{
				ID:              "openai:chat-completions",
				OfferingID:      "default",
				InteractionMode: "http-stream@v0",
				Backend: config.Backend{
					Transport: "http",
					URL:       ts.URL + "/v1/chat/completions",
				},
				Extra: map[string]any{
					"openai":            map[string]any{"model": "Qwen3.6-27B"},
					"provider":          "vllm",
					"served_model_name": "operator-visible",
					"backend_model":     "operator/backend",
					"features": map[string]any{
						"streaming": false,
					},
				},
			},
		},
	}

	catalog := newMetadataCatalog()
	refreshMetadataCatalog(context.Background(), ts.Client(), cfg, catalog)

	payload := registry.OfferingsHandler(cfg, catalog)
	req := httptest.NewRequest(http.MethodGet, "/registry/offerings", nil)
	rec := httptest.NewRecorder()
	payload.ServeHTTP(rec, req)

	var out struct {
		Capabilities []struct {
			Extra map[string]any `json:"extra"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode offerings: %v", err)
	}
	extra := out.Capabilities[0].Extra
	if got := extra["served_model_name"]; got != "operator-visible" {
		t.Fatalf("served_model_name = %#v; want operator-visible", got)
	}
	if got := extra["backend_model"]; got != "operator/backend" {
		t.Fatalf("backend_model = %#v; want operator/backend", got)
	}
	features := extra["features"].(map[string]any)
	if got := features["streaming"]; got != false {
		t.Fatalf("features.streaming = %#v; want false", got)
	}
}

func TestHydrateRunnerMetadata_SkipsWhenConfiguredModelNotFound(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{
					"id": "other-model",
				},
			},
		})
	}))
	defer ts.Close()

	cfg := &config.Config{
		Capabilities: []config.Capability{
			{
				ID:              "openai:embeddings",
				OfferingID:      "default",
				InteractionMode: "http-reqresp@v0",
				Backend: config.Backend{
					Transport: "http",
					URL:       ts.URL + "/v1/embeddings",
				},
				Extra: map[string]any{
					"openai":   map[string]any{"model": "bge-large-en-v1.5"},
					"provider": "ollama",
				},
			},
		},
	}

	catalog := newMetadataCatalog()
	refreshMetadataCatalog(context.Background(), ts.Client(), cfg, catalog)

	extra := catalog.ExtraFor("openai:embeddings", "default")
	if _, ok := extra["served_model_name"]; ok {
		t.Fatalf("served_model_name should not be set when model is missing")
	}
	if _, ok := extra["backend_model"]; ok {
		t.Fatalf("backend_model should not be set when model is missing")
	}
	if _, ok := extra["features"]; ok {
		t.Fatalf("features should not be set when model is missing")
	}
	status, ok := catalog.StatusFor("openai:embeddings", "default")
	if !ok {
		t.Fatal("expected metadata refresh status")
	}
	if status.LastResult != "model_not_found" {
		t.Fatalf("last_result = %q; want model_not_found", status.LastResult)
	}
}

func TestDeriveOpenAIModelsURL_RewritesBackendPath(t *testing.T) {
	t.Parallel()

	got, err := deriveOpenAIModelsURL("http://runner:8080/v1/chat/completions")
	if err != nil {
		t.Fatal(err)
	}
	want := "http://runner:8080/v1/models"
	if got != want {
		t.Fatalf("models URL = %s; want %s", got, want)
	}
}
