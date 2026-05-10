package responseheader

import (
	"context"
	"net/http"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
)

func TestExtract_ReadsUnsignedIntegerHeader(t *testing.T) {
	ext, err := New(map[string]any{
		"header": "X-Livepeer-Work-Units",
	})
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	got, err := ext.Extract(context.Background(), &extractors.Request{}, &extractors.Response{
		Headers: http.Header{"X-Livepeer-Work-Units": []string{"42"}},
	})
	if err != nil {
		t.Fatalf("Extract() err = %v", err)
	}
	if got != 42 {
		t.Fatalf("got %d want 42", got)
	}
}

func TestExtract_FallsBackToDefault(t *testing.T) {
	ext, err := New(map[string]any{
		"header":  "X-Livepeer-Work-Units",
		"default": 7,
	})
	if err != nil {
		t.Fatalf("New() err = %v", err)
	}

	got, err := ext.Extract(context.Background(), &extractors.Request{}, &extractors.Response{
		Headers: http.Header{},
	})
	if err != nil {
		t.Fatalf("Extract() err = %v", err)
	}
	if got != 7 {
		t.Fatalf("got %d want 7", got)
	}
}
