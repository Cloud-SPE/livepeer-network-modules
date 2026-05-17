package runner

import "strings"

// optionsConfig captures operator-supplied metadata the runner surfaces
// via /openai-text-embeddings/options. Maps 1:1 to fields the broker's
// embeddings-options discovery merges into the capability's `extra`
// block. Unset fields are omitted from the response.
type optionsConfig struct {
	servedModelName     string
	backendModel        string
	embeddingDimensions int
	maxInputTokens      int
	poolingMode         string
	upstreamKind        string // "vllm" or "ollama"
}

func optionsConfigFromEnv() optionsConfig {
	return optionsConfig{
		servedModelName:     env("SERVED_MODEL_NAME", ""),
		backendModel:        env("BACKEND_MODEL", ""),
		embeddingDimensions: envInt("EMBEDDING_DIMENSIONS", 0),
		maxInputTokens:      envInt("MAX_INPUT_TOKENS", 0),
		poolingMode:         env("POOLING_MODE", ""),
		upstreamKind:        env("UPSTREAM_KIND", "vllm"),
	}
}

// buildOptionsPayload returns the structured payload served at
// /openai-text-embeddings/options. The broker's
// embeddings-options-discovery code reads this and hydrates the
// capability's `extra` block. Operator host-config entries always win
// over discovered values; see broker discoveredOpenAIEmbeddingsExtra.
//
// Embeddings have no streaming and no reasoning/tool concerns, so the
// shape is simpler than chat: model identity, capacity (dimensions,
// max input length), and pooling strategy.
func buildOptionsPayload(models []string, cfg optionsConfig) map[string]any {
	out := map[string]any{
		"task":   "embeddings",
		"models": models,
	}
	if kind := strings.TrimSpace(cfg.upstreamKind); kind != "" {
		out["upstream_kind"] = kind
	}

	served := cfg.servedModelName
	if served == "" && len(models) > 0 {
		served = models[0]
	}
	if served != "" {
		out["served_model_name"] = served
	}
	if cfg.backendModel != "" {
		out["backend_model"] = cfg.backendModel
	}
	if cfg.embeddingDimensions > 0 {
		out["embedding_dimensions"] = cfg.embeddingDimensions
	}
	if cfg.maxInputTokens > 0 {
		out["max_input_tokens"] = cfg.maxInputTokens
	}
	if cfg.poolingMode != "" {
		out["pooling_mode"] = cfg.poolingMode
	}

	out["features"] = map[string]any{
		"streaming": false,
	}

	return out
}
