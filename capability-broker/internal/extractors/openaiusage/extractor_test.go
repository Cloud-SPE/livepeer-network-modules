package openaiusage

import (
	"context"
	"net/http"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/extractors"
)

func TestExtractJSONBody(t *testing.T) {
	ext, err := New(nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	got, err := ext.Extract(context.Background(), &extractors.Request{}, &extractors.Response{
		Body: []byte(`{"usage":{"total_tokens":182}}`),
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if got != 182 {
		t.Fatalf("Extract() = %d, want 182", got)
	}
}

func TestExtractFinalSSEUsageEvent(t *testing.T) {
	ext, err := New(map[string]any{"field": "total_tokens"})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	body := []byte("data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"id\":\"chatcmpl-1\",\"choices\":[],\"usage\":{\"prompt_tokens\":24,\"completion_tokens\":158,\"total_tokens\":182}}\n\n" +
		"data: [DONE]\n\n")
	got, err := ext.Extract(context.Background(), &extractors.Request{Headers: make(http.Header)}, &extractors.Response{
		Body: body,
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if got != 182 {
		t.Fatalf("Extract() = %d, want 182", got)
	}
}

func TestExtractReturnsZeroWhenSSEHasNoUsage(t *testing.T) {
	ext, err := New(nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	body := []byte("data: {\"id\":\"chatcmpl-1\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: [DONE]\n\n")
	got, err := ext.Extract(context.Background(), &extractors.Request{}, &extractors.Response{
		Body: body,
	})
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if got != 0 {
		t.Fatalf("Extract() = %d, want 0", got)
	}
}
