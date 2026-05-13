package adminapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
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
	return setupServerWithTokens(t, nil)
}

func setupServerWithTokens(t *testing.T, adminTokens []string) (*Server, WebDeps) {
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

	srv := New("127.0.0.1:0", slog.Default(), adminTokens)
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

func TestWebRoutes_AuthLoginRequired(t *testing.T) {
	srv, _ := setupServerWithTokens(t, []string{"admin-token"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)

	resp, err := http.Get("http://" + srv.Addr() + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Request.URL.Path != "/login" {
		t.Fatalf("expected redirect to /login, got %s", resp.Request.URL.Path)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Jar:           jar,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	loginResp, err := client.Post(
		"http://"+srv.Addr()+"/login",
		"application/x-www-form-urlencoded",
		strings.NewReader("admin_token=admin-token&actor=operator1"),
	)
	if err != nil {
		t.Fatal(err)
	}
	loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", loginResp.StatusCode)
	}
	if got := loginResp.Header.Get("Set-Cookie"); !strings.Contains(got, "Max-Age=43200") {
		t.Fatalf("expected 12h session cookie, got %q", got)
	}

	resp, err = client.Get("http://" + srv.Addr() + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "actor operator1") {
		t.Fatalf("expected actor banner, got %s", body)
	}
}

func TestWebRoutes_RefreshRosterRunsScrapeAndShowsFlash(t *testing.T) {
	srv, _ := setupServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)

	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+"/refresh-roster", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d", resp.StatusCode)
	}
	loc := resp.Header.Get("Location")
	if !strings.Contains(loc, "refresh_outcome=accepted") {
		t.Fatalf("unexpected redirect %q", loc)
	}

	resp, err = http.Get("http://" + srv.Addr() + loc)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "fetched latest broker state and rebuilt candidate") {
		t.Fatalf("missing refresh flash: %s", body)
	}
}
