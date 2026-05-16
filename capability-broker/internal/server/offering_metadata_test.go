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
			"task":          "speech",
			"formats":       map[string]any{"output": []string{"mp3", "wav", "pcm"}},
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

	audio, ok := cfg.Capabilities[0].Extra["audio"].(map[string]any)
	if !ok {
		t.Fatalf("extra.audio missing or wrong type: %#v", cfg.Capabilities[0].Extra["audio"])
	}
	if got := audio["task"]; got != "speech" {
		t.Fatalf("audio.task = %#v; want speech", got)
	}
	voices, ok := audio["voices"].(map[string]any)
	if !ok {
		t.Fatalf("extra.audio.voices missing or wrong type: %#v", audio["voices"])
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
	formats, ok := audio["formats"].(map[string]any)
	if !ok {
		t.Fatalf("audio.formats missing or wrong type: %#v", audio["formats"])
	}
	output, ok := formats["output"].([]string)
	if !ok || len(output) != 3 {
		t.Fatalf("audio.formats.output = %#v", formats["output"])
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

	if audio, ok := cfg.Capabilities[0].Extra["audio"].(map[string]any); ok {
		if _, exists := audio["voices"]; exists {
			t.Fatalf("voices should not be set on fetch failure")
		}
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

func TestHydrateRunnerMetadata_PopulatesVideoTranscodeMetadata(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != videoTranscodePresetsPath {
			t.Fatalf("path = %s; want %s", r.URL.Path, videoTranscodePresetsPath)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"gpu_vendor": "nvidia",
			"presets": []map[string]any{
				{"name": "h264-1080p", "video_codec": "h264"},
				{"name": "hevc-1080p", "video_codec": "hevc"},
			},
		})
	}))
	defer ts.Close()

	cfg := &config.Config{
		Capabilities: []config.Capability{
			{
				ID:              "video:transcode.vod",
				OfferingID:      "vod-default",
				InteractionMode: "http-reqresp@v0",
				Backend: config.Backend{
					Transport: "http",
					URL:       ts.URL + "/v1/video/transcode",
				},
				Extra: map[string]any{
					"provider": "transcode-runner",
					"video":    map[string]any{"task": "transcode"},
				},
			},
		},
	}

	hydrateRunnerMetadataWithClient(context.Background(), ts.Client(), cfg)

	video, ok := cfg.Capabilities[0].Extra["video"].(map[string]any)
	if !ok {
		t.Fatalf("extra.video missing or wrong type: %#v", cfg.Capabilities[0].Extra["video"])
	}
	presets, ok := video["presets"].([]string)
	if !ok || len(presets) != 2 || presets[0] != "h264-1080p" || presets[1] != "hevc-1080p" {
		t.Fatalf("video.presets = %#v", video["presets"])
	}
	codecs, ok := video["codecs"].([]string)
	if !ok || len(codecs) != 2 || codecs[0] != "h264" || codecs[1] != "hevc" {
		t.Fatalf("video.codecs = %#v", video["codecs"])
	}
	packaging, ok := video["packaging"].([]string)
	if !ok || len(packaging) != 1 || packaging[0] != "mp4" {
		t.Fatalf("video.packaging = %#v", video["packaging"])
	}
	hardware, ok := video["hardware"].(map[string]any)
	if !ok {
		t.Fatalf("video.hardware missing or wrong type: %#v", video["hardware"])
	}
	if got := hardware["gpu_vendor"]; got != "nvidia" {
		t.Fatalf("video.hardware.gpu_vendor = %#v; want nvidia", got)
	}
}

func TestHydrateRunnerMetadata_PopulatesVideoABRMetadata(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != videoABRPresetsPath {
			t.Fatalf("path = %s; want %s", r.URL.Path, videoABRPresetsPath)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"gpu_vendor": "intel",
			"presets": []map[string]any{
				{
					"name":   "abr-standard",
					"format": "hls",
					"renditions": []map[string]any{
						{"video": map[string]any{"codec": "h264"}},
						{"video": map[string]any{"codec": "hevc"}},
					},
				},
			},
		})
	}))
	defer ts.Close()

	cfg := &config.Config{
		Capabilities: []config.Capability{
			{
				ID:              "video:transcode.abr",
				OfferingID:      "abr-default",
				InteractionMode: "http-reqresp@v0",
				Backend: config.Backend{
					Transport: "http",
					URL:       ts.URL + "/v1/video/transcode/abr",
				},
				Extra: map[string]any{
					"provider": "abr-runner",
					"video":    map[string]any{"task": "abr-transcode"},
				},
			},
		},
	}

	hydrateRunnerMetadataWithClient(context.Background(), ts.Client(), cfg)

	video, ok := cfg.Capabilities[0].Extra["video"].(map[string]any)
	if !ok {
		t.Fatalf("extra.video missing or wrong type: %#v", cfg.Capabilities[0].Extra["video"])
	}
	presets, ok := video["presets"].([]string)
	if !ok || len(presets) != 1 || presets[0] != "abr-standard" {
		t.Fatalf("video.presets = %#v", video["presets"])
	}
	codecs, ok := video["codecs"].([]string)
	if !ok || len(codecs) != 2 || codecs[0] != "h264" || codecs[1] != "hevc" {
		t.Fatalf("video.codecs = %#v", video["codecs"])
	}
	packaging, ok := video["packaging"].([]string)
	if !ok || len(packaging) != 1 || packaging[0] != "hls" {
		t.Fatalf("video.packaging = %#v", video["packaging"])
	}
	hardware, ok := video["hardware"].(map[string]any)
	if !ok {
		t.Fatalf("video.hardware missing or wrong type: %#v", video["hardware"])
	}
	if got := hardware["gpu_vendor"]; got != "intel" {
		t.Fatalf("video.hardware.gpu_vendor = %#v; want intel", got)
	}
}

func TestHydrateRunnerMetadata_PopulatesVTuberMetadata(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != vtuberOptionsPath {
			t.Fatalf("path = %s; want %s", r.URL.Path, vtuberOptionsPath)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"capabilities":   []string{"livepeer:vtuber-session"},
			"renderer":       "chromium",
			"task":           "session",
			"control_schema": "vtuber-control/v1",
			"media_schema":   "trickle-segment-stream/v1",
			"features": map[string]any{
				"renderer_control": true,
				"status_polling":   true,
				"trickle_publish":  true,
				"youtube_egress":   true,
			},
		})
	}))
	defer ts.Close()

	cfg := &config.Config{
		Capabilities: []config.Capability{
			{
				ID:              "livepeer:vtuber-session",
				OfferingID:      "vtuber-default",
				InteractionMode: "session-control-plus-media@v0",
				Backend: config.Backend{
					Transport: "http",
					URL:       ts.URL + "/api/sessions/start",
				},
				Extra: map[string]any{
					"provider": "vtuber-runner",
					"vtuber":   map[string]any{"task": "session"},
				},
			},
		},
	}

	hydrateRunnerMetadataWithClient(context.Background(), ts.Client(), cfg)

	vtuber, ok := cfg.Capabilities[0].Extra["vtuber"].(map[string]any)
	if !ok {
		t.Fatalf("extra.vtuber missing or wrong type: %#v", cfg.Capabilities[0].Extra["vtuber"])
	}
	if got := vtuber["control_schema"]; got != "vtuber-control/v1" {
		t.Fatalf("vtuber.control_schema = %#v; want vtuber-control/v1", got)
	}
	if got := vtuber["media_schema"]; got != "trickle-segment-stream/v1" {
		t.Fatalf("vtuber.media_schema = %#v; want trickle-segment-stream/v1", got)
	}
	features, ok := vtuber["features"].(map[string]bool)
	if !ok {
		t.Fatalf("vtuber.features missing or wrong type: %#v", vtuber["features"])
	}
	if !features["renderer_control"] || !features["status_polling"] || !features["trickle_publish"] || !features["youtube_egress"] {
		t.Fatalf("vtuber.features = %#v", features)
	}
}

func TestRefreshMetadataCatalog_PopulatesVTuberMetadataStatus(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != vtuberOptionsPath {
			t.Fatalf("path = %s; want %s", r.URL.Path, vtuberOptionsPath)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"capabilities":   []string{"livepeer:vtuber-session"},
			"renderer":       "chromium",
			"task":           "session",
			"control_schema": "vtuber-control/v1",
			"media_schema":   "trickle-segment-stream/v1",
			"features": map[string]any{
				"renderer_control": true,
				"status_polling":   true,
			},
		})
	}))
	defer ts.Close()

	cfg := &config.Config{
		Capabilities: []config.Capability{
			{
				ID:              "livepeer:vtuber-session",
				OfferingID:      "vtuber-default",
				InteractionMode: "session-control-plus-media@v0",
				Backend: config.Backend{
					Transport: "http",
					URL:       ts.URL + "/api/sessions/start",
				},
				Extra: map[string]any{
					"provider": "vtuber-runner",
					"vtuber":   map[string]any{"task": "session"},
				},
			},
		},
	}

	catalog := newMetadataCatalog()
	refreshMetadataCatalog(context.Background(), ts.Client(), cfg, catalog)

	extra := catalog.ExtraFor("livepeer:vtuber-session", "vtuber-default")
	vtuber, ok := extra["vtuber"].(map[string]any)
	if !ok {
		t.Fatalf("catalog vtuber extra missing: %#v", extra["vtuber"])
	}
	if got := vtuber["control_schema"]; got != "vtuber-control/v1" {
		t.Fatalf("vtuber.control_schema = %#v; want vtuber-control/v1", got)
	}
	status, ok := catalog.StatusFor("livepeer:vtuber-session", "vtuber-default")
	if !ok {
		t.Fatal("expected metadata refresh status")
	}
	if status.Provider != "vtuber-runner" {
		t.Fatalf("provider = %q; want vtuber-runner", status.Provider)
	}
	if status.LastResult != "enriched" {
		t.Fatalf("last_result = %q; want enriched", status.LastResult)
	}
	if status.LastSuccessAt.IsZero() {
		t.Fatal("last_success_at should be populated")
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

func TestHydrateRunnerMetadata_PopulatesOpenAIAudioFormats(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != audioTranscriptionsOptionsPath {
			t.Fatalf("path = %s; want %s", r.URL.Path, audioTranscriptionsOptionsPath)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []string{"whisper-large-v3"},
			"task":   "transcription",
			"formats": map[string]any{
				"input":  []string{"mp3", "wav", "m4a", "flac"},
				"output": []string{"json", "text", "srt", "vtt", "verbose_json"},
			},
		})
	}))
	defer ts.Close()

	cfg := &config.Config{
		Capabilities: []config.Capability{
			{
				ID:              "openai:audio-transcriptions",
				OfferingID:      "whisper-large-v3",
				InteractionMode: "http-multipart@v0",
				Backend: config.Backend{
					Transport: "http",
					URL:       ts.URL + "/v1/audio/transcriptions",
				},
				Extra: map[string]any{
					"openai":   map[string]any{"model": "whisper-large-v3"},
					"provider": "openai-audio-runner",
					"audio":    map[string]any{"task": "transcription"},
				},
			},
		},
	}

	hydrateRunnerMetadataWithClient(context.Background(), ts.Client(), cfg)

	audio, ok := cfg.Capabilities[0].Extra["audio"].(map[string]any)
	if !ok {
		t.Fatalf("audio extra missing: %#v", cfg.Capabilities[0].Extra["audio"])
	}
	formats, ok := audio["formats"].(map[string]any)
	if !ok {
		t.Fatalf("audio.formats missing: %#v", audio["formats"])
	}
	input, ok := formats["input"].([]string)
	if !ok || len(input) != 4 {
		t.Fatalf("audio.formats.input = %#v", formats["input"])
	}
	output, ok := formats["output"].([]string)
	if !ok || len(output) != 5 {
		t.Fatalf("audio.formats.output = %#v", formats["output"])
	}
}
