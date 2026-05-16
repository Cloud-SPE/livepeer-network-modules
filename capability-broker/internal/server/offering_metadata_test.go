package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
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

	hydrateRunnerMetadata(context.Background(), cfg)

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
