package runner

import (
	"reflect"
	"testing"
)

func TestBuildOptionsPayload_Minimal(t *testing.T) {
	out := buildOptionsPayload([]string{"M"}, optionsConfig{})
	if out["task"] != "chat" {
		t.Fatalf("task = %v", out["task"])
	}
	if !reflect.DeepEqual(out["models"], []string{"M"}) {
		t.Fatalf("models = %v", out["models"])
	}
	if out["served_model_name"] != "M" {
		t.Fatalf("served_model_name should default to first model; got %v", out["served_model_name"])
	}
	if _, ok := out["backend_model"]; ok {
		t.Fatal("backend_model should be omitted when unset")
	}
	if _, ok := out["context_length"]; ok {
		t.Fatal("context_length should be omitted when zero")
	}
	if _, ok := out["parsers"]; ok {
		t.Fatal("parsers should be omitted when no parsers declared")
	}
	features, ok := out["features"].(map[string]any)
	if !ok {
		t.Fatalf("features missing or wrong type: %T", out["features"])
	}
	if features["streaming"] != true {
		t.Fatal("streaming feature should always be true")
	}
	if features["include_usage_required"] != true {
		t.Fatal("include_usage_required feature should always be true")
	}
	if _, ok := features["tool_calling"]; ok {
		t.Fatal("tool_calling should not be set when TOOL_CALL_PARSER is empty")
	}
	if _, ok := features["reasoning"]; ok {
		t.Fatal("reasoning should not be set when REASONING_PARSER is empty")
	}
}

func TestBuildOptionsPayload_Full(t *testing.T) {
	cfg := optionsConfig{
		servedModelName: "Qwen3.6-27B",
		backendModel:    "sakamakismile/Qwen3.6-27B-Text-NVFP4-MTP",
		contextLength:   196608,
		reasoningParser: "qwen3",
		toolCallParser:  "qwen3_coder",
		quantization:    "modelopt",
	}
	out := buildOptionsPayload([]string{"Qwen3.6-27B"}, cfg)
	if out["served_model_name"] != "Qwen3.6-27B" {
		t.Fatalf("served_model_name = %v", out["served_model_name"])
	}
	if out["backend_model"] != "sakamakismile/Qwen3.6-27B-Text-NVFP4-MTP" {
		t.Fatalf("backend_model = %v", out["backend_model"])
	}
	if out["context_length"] != 196608 {
		t.Fatalf("context_length = %v", out["context_length"])
	}
	if out["quantization"] != "modelopt" {
		t.Fatalf("quantization = %v", out["quantization"])
	}
	parsers, ok := out["parsers"].(map[string]any)
	if !ok {
		t.Fatalf("parsers missing or wrong type: %T", out["parsers"])
	}
	if parsers["reasoning"] != "qwen3" {
		t.Fatalf("reasoning parser = %v", parsers["reasoning"])
	}
	if parsers["tool_call"] != "qwen3_coder" {
		t.Fatalf("tool_call parser = %v", parsers["tool_call"])
	}
	features := out["features"].(map[string]any)
	if features["tool_calling"] != true {
		t.Fatal("tool_calling should be true when TOOL_CALL_PARSER is set")
	}
	if features["reasoning"] != true {
		t.Fatal("reasoning should be true when REASONING_PARSER is set")
	}
}

func TestBuildOptionsPayload_OperatorOverridesDiscoveredModel(t *testing.T) {
	cfg := optionsConfig{servedModelName: "operator-chosen"}
	out := buildOptionsPayload([]string{"discovered-from-vllm"}, cfg)
	if out["served_model_name"] != "operator-chosen" {
		t.Fatalf("operator-set SERVED_MODEL_NAME should win over discovery; got %v", out["served_model_name"])
	}
}
