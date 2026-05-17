package runner

import "testing"

const vllmEmbeddingsFixture = `{
  "object": "list",
  "data": [
    {"object":"embedding","index":0,"embedding":[0.1,0.2,0.3]}
  ],
  "model": "bge-large-en-v1.5",
  "usage": {"prompt_tokens": 17, "total_tokens": 17}
}`

const ollamaEmbeddingsFixture = `{
  "object": "list",
  "data": [
    {"object":"embedding","index":0,"embedding":[-0.05,0.91,0.04]}
  ],
  "model": "nomic-embed-text",
  "usage": {"prompt_tokens": 8, "total_tokens": 8}
}`

func TestExtractUsage_VLLMShape(t *testing.T) {
	if got := extractUsage([]byte(vllmEmbeddingsFixture), "total_tokens"); got != 17 {
		t.Fatalf("got %d, want 17", got)
	}
	if got := extractUsage([]byte(vllmEmbeddingsFixture), "prompt_tokens"); got != 17 {
		t.Fatalf("prompt_tokens got %d, want 17", got)
	}
}

func TestExtractUsage_OllamaShape(t *testing.T) {
	// Same OpenAI-compat envelope; runner is upstream-agnostic.
	if got := extractUsage([]byte(ollamaEmbeddingsFixture), "total_tokens"); got != 8 {
		t.Fatalf("got %d, want 8", got)
	}
}

func TestExtractUsage_MissingUsageReturnsZero(t *testing.T) {
	body := []byte(`{"object":"list","data":[]}`)
	if got := extractUsage(body, "total_tokens"); got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
}

func TestExtractUsage_InvalidJSONReturnsZero(t *testing.T) {
	if got := extractUsage([]byte("not json"), "total_tokens"); got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
}

func TestExtractUsage_EmptyBodyReturnsZero(t *testing.T) {
	if got := extractUsage([]byte(""), "total_tokens"); got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
}
