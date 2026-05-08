package manifestfetcher

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

func TestMeteredFetcher_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()

	rec := metrics.NewCounter()
	f := WithMetrics(New(Config{MaxBytes: 1024, Timeout: 2 * time.Second, AllowInsecure: true}), rec)
	if _, err := f.Fetch(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	if rec.ManifestFetches.Load() != 1 {
		t.Fatalf("fetches = %d", rec.ManifestFetches.Load())
	}
	if got := rec.LastFetchOutcome.Load(); got != metrics.OutcomeOK {
		t.Fatalf("last outcome = %v", got)
	}
}

func TestMeteredFetcher_TooLarge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("x", 4096)))
	}))
	defer srv.Close()

	rec := metrics.NewCounter()
	f := WithMetrics(New(Config{MaxBytes: 100, Timeout: 2 * time.Second, AllowInsecure: true}), rec)
	_, err := f.Fetch(context.Background(), srv.URL)
	if !errors.Is(err, types.ErrManifestTooLarge) {
		t.Fatal(err)
	}
	if got := rec.LastFetchOutcome.Load(); got != metrics.OutcomeTooLarge {
		t.Fatalf("last outcome = %v", got)
	}
}

func TestMeteredFetcher_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer srv.Close()

	rec := metrics.NewCounter()
	f := WithMetrics(New(Config{MaxBytes: 1024, Timeout: 1 * time.Second, AllowInsecure: true}), rec)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := f.Fetch(ctx, srv.URL); err == nil {
		t.Fatal("expected timeout")
	}
	got := rec.LastFetchOutcome.Load()
	// Either timeout (ctx hit first) or http_error (transport reported context deadline).
	if got != metrics.OutcomeTimeout && got != metrics.OutcomeHTTPError {
		t.Fatalf("unexpected outcome %v", got)
	}
}

func TestMeteredFetcher_NilRecorderReturnsInner(t *testing.T) {
	inner := New(Config{})
	if got := WithMetrics(inner, nil); got != inner {
		t.Fatal("nil recorder should return inner fetcher unchanged")
	}
}
