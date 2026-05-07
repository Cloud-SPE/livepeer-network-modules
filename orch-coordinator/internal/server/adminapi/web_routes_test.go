package adminapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/candidates"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/published"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/candidate"
)

func setupServer(t *testing.T) (*Server, WebDeps) {
	t.Helper()
	dir := t.TempDir()
	store, err := candidates.New(filepath.Join(dir, "c"), 0)
	if err != nil {
		t.Fatal(err)
	}
	scrapeSvc := primedScrapeService(t)
	builder, err := candidate.NewBuilder(scrapeSvc, store, candidate.BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    time.Hour,
	}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Rebuild(); err != nil {
		t.Fatal(err)
	}
	pubStore, err := published.New(filepath.Join(dir, "p"))
	if err != nil {
		t.Fatal(err)
	}
	auditLog, err := audit.Open(filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { auditLog.Close() })

	srv := New("127.0.0.1:0", slog.Default())
	deps := WebDeps{
		Builder:        builder,
		Scrape:         scrapeSvc,
		Published:      pubStore,
		Audit:          auditLog,
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Version:        "test",
	}
	if err := srv.WebRoutes(deps); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	return srv, deps
}

func TestWebRoutes_RosterRenders(t *testing.T) {
	srv, _ := setupServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	resp, err := http.Get("http://" + srv.Addr() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "Roster") {
		t.Fatalf("expected Roster heading, got %s", body)
	}
	if !strings.Contains(string(body), "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatalf("expected orch address in header")
	}
}

func TestWebRoutes_DiffRenders(t *testing.T) {
	srv, _ := setupServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	resp, err := http.Get("http://" + srv.Addr() + "/diff")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "Diff") {
		t.Fatalf("body=%s", body)
	}
}

func TestWebRoutes_AuditRenders(t *testing.T) {
	srv, _ := setupServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	resp, err := http.Get("http://" + srv.Addr() + "/audit")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "Audit") {
		t.Fatalf("body=%s", body)
	}
}

func TestWebRoutes_AssetsServed(t *testing.T) {
	srv, _ := setupServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	resp, err := http.Get("http://" + srv.Addr() + "/assets/style.css")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/css") {
		t.Fatalf("content-type: %s", ct)
	}
}
