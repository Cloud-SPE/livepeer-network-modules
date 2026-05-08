package manifestfetcher

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

func TestHTTP_Fetch_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	f := New(Config{MaxBytes: 1024, Timeout: 2 * time.Second, AllowInsecure: true})
	body, err := f.Fetch(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %s", body)
	}
}

func TestHTTP_Fetch_TooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 4096)))
	}))
	defer srv.Close()

	f := New(Config{MaxBytes: 1024, Timeout: 2 * time.Second, AllowInsecure: true})
	_, err := f.Fetch(context.Background(), srv.URL)
	if !errors.Is(err, types.ErrManifestTooLarge) {
		t.Fatalf("expected ErrManifestTooLarge, got %v", err)
	}
}

func TestHTTP_Fetch_HTTPNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	f := New(Config{MaxBytes: 1024, Timeout: 2 * time.Second, AllowInsecure: true})
	_, err := f.Fetch(context.Background(), srv.URL)
	if !errors.Is(err, types.ErrManifestUnavailable) {
		t.Fatalf("expected ErrManifestUnavailable, got %v", err)
	}
}

func TestHTTP_Fetch_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer srv.Close()

	f := New(Config{MaxBytes: 1024, Timeout: 50 * time.Millisecond, AllowInsecure: true})
	_, err := f.Fetch(context.Background(), srv.URL)
	if !errors.Is(err, types.ErrManifestUnavailable) {
		t.Fatalf("expected ErrManifestUnavailable, got %v", err)
	}
}

func TestStatic_FetchReturnsBody(t *testing.T) {
	s := &Static{
		Bodies: map[string][]byte{"http://x/y": []byte("hi")},
	}
	body, err := s.Fetch(context.Background(), "http://x/y")
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hi" {
		t.Fatalf("body = %s", body)
	}

	if _, err := s.Fetch(context.Background(), "http://x/missing"); !errors.Is(err, types.ErrManifestUnavailable) {
		t.Fatalf("expected ErrManifestUnavailable, got %v", err)
	}
}

func TestStatic_FetchInjectedError(t *testing.T) {
	s := &Static{
		Errors: map[string]error{"http://x/y": types.ErrChainUnavailable},
	}
	if _, err := s.Fetch(context.Background(), "http://x/y"); !errors.Is(err, types.ErrChainUnavailable) {
		t.Fatalf("expected injected error, got %v", err)
	}
}

func TestStatic_NilSafe(t *testing.T) {
	var s *Static
	if _, err := s.Fetch(context.Background(), "x"); !errors.Is(err, types.ErrManifestUnavailable) {
		t.Fatalf("nil static: expected ErrManifestUnavailable, got %v", err)
	}
}
