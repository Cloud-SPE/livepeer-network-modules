package metrics

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestRegistry_NewDoesNotPanic(t *testing.T) {
	r := New()
	r.ObserveScrape("b1", "ok", 100*time.Millisecond)
	r.ObserveCandidateBuild("ok", 5*time.Millisecond)
	r.ObserveUpload("accepted", 0)
	r.ObservePublish("accepted")
	r.ObserveBrokerCounts(2, 1)
	r.SetManifestState(120, 5)
	r.SetDriftCount("price_changed", 2)
}

func TestServer_ServesMetrics(t *testing.T) {
	r := New()
	r.ObserveScrape("b1", "ok", time.Second)
	srv := NewServer("127.0.0.1:0", r, slog.Default())
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)

	resp, err := http.Get("http://" + srv.Addr() + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "orch_coordinator_scrape_total") {
		t.Fatalf("metric missing from output: %s", body)
	}
}

func TestServer_HealthzReturns200(t *testing.T) {
	r := New()
	srv := NewServer("127.0.0.1:0", r, slog.Default())
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	resp, err := http.Get("http://" + srv.Addr() + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
