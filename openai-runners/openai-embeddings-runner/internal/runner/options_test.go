package runner

import (
	"reflect"
	"testing"
)

func TestBuildOptionsPayload_Minimal(t *testing.T) {
	out := buildOptionsPayload([]string{"M"}, optionsConfig{})
	if out["task"] != "embeddings" {
		t.Fatalf("task = %v", out["task"])
	}
	if !reflect.DeepEqual(out["models"], []string{"M"}) {
		t.Fatalf("models = %v", out["models"])
	}
	if out["served_model_name"] != "M" {
		t.Fatalf("served_model_name should default to first model; got %v", out["served_model_name"])
	}
	for _, key := range []string{"backend_model", "embedding_dimensions", "max_input_tokens", "pooling_mode", "upstream_kind"} {
		if _, present := out[key]; present {
			t.Errorf("%s should be omitted when unset; got %v", key, out[key])
		}
	}
	features := out["features"].(map[string]any)
	if features["streaming"] != false {
		t.Fatal("streaming feature should be false for embeddings")
	}
}

func TestBuildOptionsPayload_Full(t *testing.T) {
	cfg := optionsConfig{
		servedModelName:     "bge-large-en-v1.5",
		backendModel:        "BAAI/bge-large-en-v1.5",
		embeddingDimensions: 1024,
		maxInputTokens:      512,
		poolingMode:         "cls",
		upstreamKind:        "vllm",
	}
	out := buildOptionsPayload([]string{"bge-large-en-v1.5"}, cfg)
	if out["served_model_name"] != "bge-large-en-v1.5" {
		t.Fatalf("served_model_name = %v", out["served_model_name"])
	}
	if out["backend_model"] != "BAAI/bge-large-en-v1.5" {
		t.Fatalf("backend_model = %v", out["backend_model"])
	}
	if out["embedding_dimensions"] != 1024 {
		t.Fatalf("embedding_dimensions = %v", out["embedding_dimensions"])
	}
	if out["max_input_tokens"] != 512 {
		t.Fatalf("max_input_tokens = %v", out["max_input_tokens"])
	}
	if out["pooling_mode"] != "cls" {
		t.Fatalf("pooling_mode = %v", out["pooling_mode"])
	}
	if out["upstream_kind"] != "vllm" {
		t.Fatalf("upstream_kind = %v", out["upstream_kind"])
	}
}

func TestBuildOptionsPayload_OperatorOverridesDiscoveredModel(t *testing.T) {
	cfg := optionsConfig{servedModelName: "operator-chosen"}
	out := buildOptionsPayload([]string{"discovered"}, cfg)
	if out["served_model_name"] != "operator-chosen" {
		t.Fatalf("operator SERVED_MODEL_NAME should win; got %v", out["served_model_name"])
	}
}

func TestBuildOptionsPayload_OllamaKind(t *testing.T) {
	out := buildOptionsPayload([]string{"nomic-embed-text"}, optionsConfig{upstreamKind: "ollama"})
	if out["upstream_kind"] != "ollama" {
		t.Fatalf("upstream_kind = %v", out["upstream_kind"])
	}
}
