package responsetrailer

import (
	"context"
	"net/http"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
)

func build(t *testing.T, cfg map[string]any) extractors.Extractor {
	t.Helper()
	e, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func TestExtract_ReadsNumericTrailer(t *testing.T) {
	e := build(t, map[string]any{"trailer": "X-Livepeer-Work-Units"})
	resp := &extractors.Response{
		Trailers: http.Header{"X-Livepeer-Work-Units": []string{"42"}},
	}
	got, err := e.Extract(context.Background(), nil, resp)
	if err != nil {
		t.Fatal(err)
	}
	if got != 42 {
		t.Fatalf("got %d, want 42", got)
	}
}

func TestExtract_MissingTrailerFallsBackToDefault(t *testing.T) {
	e := build(t, map[string]any{"trailer": "X-Livepeer-Work-Units", "default": 7})
	resp := &extractors.Response{Trailers: http.Header{}}
	got, err := e.Extract(context.Background(), nil, resp)
	if err != nil {
		t.Fatal(err)
	}
	if got != 7 {
		t.Fatalf("got %d, want 7", got)
	}
}

func TestExtract_NilTrailersFallsBackToDefault(t *testing.T) {
	e := build(t, map[string]any{"trailer": "X-Livepeer-Work-Units", "default": 5})
	got, err := e.Extract(context.Background(), nil, &extractors.Response{})
	if err != nil {
		t.Fatal(err)
	}
	if got != 5 {
		t.Fatalf("got %d, want 5", got)
	}
}

func TestExtract_NonNumericTrailerFallsBackToDefault(t *testing.T) {
	e := build(t, map[string]any{"trailer": "X-Livepeer-Work-Units", "default": 3})
	resp := &extractors.Response{Trailers: http.Header{"X-Livepeer-Work-Units": []string{"not-a-number"}}}
	got, err := e.Extract(context.Background(), nil, resp)
	if err != nil {
		t.Fatal(err)
	}
	if got != 3 {
		t.Fatalf("got %d, want 3", got)
	}
}

func TestNew_RequiresTrailerField(t *testing.T) {
	if _, err := New(map[string]any{}); err == nil {
		t.Fatal("expected error when trailer is missing")
	}
	if _, err := New(map[string]any{"trailer": ""}); err == nil {
		t.Fatal("expected error when trailer is empty")
	}
}

func TestNew_RejectsNegativeDefault(t *testing.T) {
	if _, err := New(map[string]any{"trailer": "X", "default": -1}); err == nil {
		t.Fatal("expected error for negative default")
	}
}
