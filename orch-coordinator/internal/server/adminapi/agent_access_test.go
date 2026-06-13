package adminapi

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/repo/candidates"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/service/candidate"
)

func builtCandidateServer(t *testing.T, adminTokens []string) (*Server, *candidate.Builder, *audit.Log) {
	t.Helper()
	store, err := candidates.New(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	builder, err := candidate.NewBuilder(primedScrapeService(t), store, candidate.BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    time.Hour,
	}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Rebuild(); err != nil {
		t.Fatal(err)
	}
	log, err := audit.Open(t.TempDir() + "/audit.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { log.Close() })
	srv := New("127.0.0.1:0", slog.Default(), adminTokens)
	srv.CandidateRoutes(builder, store, log)
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go srv.Serve(ctx)
	return srv, builder, log
}

func TestCandidateRoutes_ETagConditionalGet(t *testing.T) {
	srv, builder, _ := builtCandidateServer(t, nil)

	wantETag := `"` + candidate.SHA256Hex(builder.Latest().ManifestBytes) + `"`
	for _, path := range []string{"/candidate.json", "/candidate.tar.gz"} {
		resp, err := http.Get("http://" + srv.Addr() + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status=%d", path, resp.StatusCode)
		}
		if len(body) == 0 {
			t.Fatalf("%s: empty body", path)
		}
		if got := resp.Header.Get("ETag"); got != wantETag {
			t.Fatalf("%s: etag=%q want %q", path, got, wantETag)
		}

		req, _ := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+path, nil)
		req.Header.Set("If-None-Match", resp.Header.Get("ETag"))
		resp2, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s conditional: %v", path, err)
		}
		body2, _ := io.ReadAll(resp2.Body)
		resp2.Body.Close()
		if resp2.StatusCode != http.StatusNotModified {
			t.Fatalf("%s conditional: status=%d want 304", path, resp2.StatusCode)
		}
		if len(body2) != 0 {
			t.Fatalf("%s conditional: 304 carried a body", path)
		}
		if got := resp2.Header.Get("ETag"); got != wantETag {
			t.Fatalf("%s conditional: etag=%q want %q", path, got, wantETag)
		}
	}

	// A stale validator must miss and return the full body.
	req, _ := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/candidate.json", nil)
	req.Header.Set("If-None-Match", `"0xdeadbeef"`)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stale validator: status=%d want 200", resp.StatusCode)
	}
}

func TestCandidateRoutes_304DoesNotAudit(t *testing.T) {
	srv, _, log := builtCandidateServer(t, nil)

	resp, err := http.Get("http://" + srv.Addr() + "/candidate.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	etag := resp.Header.Get("ETag")
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	before, err := log.Recent(50)
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/candidate.tar.gz", nil)
	req.Header.Set("If-None-Match", etag)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotModified {
		t.Fatalf("status=%d want 304", resp2.StatusCode)
	}

	after, err := log.Recent(50)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("304 appended an audit event: before=%d after=%d", len(before), len(after))
	}
}

func TestCandidateRoutes_NotReadySetsRetryAfter(t *testing.T) {
	store, err := candidates.New(t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	builder, err := candidate.NewBuilder(emptyScrapeService(t), store, candidate.BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    time.Hour,
	}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	srv := New("127.0.0.1:0", slog.Default(), nil)
	srv.CandidateRoutes(builder, store, nil)
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)

	for _, path := range []string{"/candidate.json", "/candidate.tar.gz"} {
		resp, err := http.Get("http://" + srv.Addr() + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("%s: status=%d want 503", path, resp.StatusCode)
		}
		if got := resp.Header.Get("Retry-After"); got != candidateRetryAfterSeconds {
			t.Fatalf("%s: retry-after=%q", path, got)
		}
	}
}

func TestAgentBearer_AdmitsCandidateDownloadAndAuditsAsAgent(t *testing.T) {
	srv, _, log := builtCandidateServer(t, []string{"admin-token"})
	srv.SetAgentToken("agent-secret")

	req, _ := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/candidate.tar.gz", nil)
	req.Header.Set("Authorization", "Bearer agent-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d want 200", resp.StatusCode)
	}

	events, err := log.Recent(5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if ev.Outcome == audit.OutcomeCandidateDownloaded {
			found = true
			if ev.Actor != agentActor {
				t.Fatalf("actor=%q want %q", ev.Actor, agentActor)
			}
		}
	}
	if !found {
		t.Fatal("expected candidate download audit event")
	}
}

func TestAgentBearer_WrongTokenRejectedOutright(t *testing.T) {
	srv, _, _ := builtCandidateServer(t, []string{"admin-token"})
	srv.SetAgentToken("agent-secret")

	client := &http.Client{CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}

	req, _ := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/candidate.json", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong bearer: status=%d want 401 (must not fall through to the login flow)", resp.StatusCode)
	}

	// Without any credential, the GET still redirects to login.
	resp2, err := client.Get("http://" + srv.Addr() + "/candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusSeeOther {
		t.Fatalf("no credential: status=%d want 303 redirect", resp2.StatusCode)
	}
}

func TestAgentBearer_DisabledRejectsBearer(t *testing.T) {
	srv, _, _ := builtCandidateServer(t, []string{"admin-token"})

	req, _ := http.NewRequest(http.MethodGet, "http://"+srv.Addr()+"/candidate.json", nil)
	req.Header.Set("Authorization", "Bearer agent-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bearer with no token configured: status=%d want 401", resp.StatusCode)
	}
}

func TestAgentBearer_AdmitsSignedManifestUpload(t *testing.T) {
	rec, auditLog, priv, addr := newRecv(t)
	srv := New("127.0.0.1:0", slog.Default(), []string{"admin-token"})
	srv.SetAgentToken("agent-secret")
	srv.UploadRoutes(rec)
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)

	body := signedBody(t, priv, addr, 1)
	req, _ := http.NewRequest(http.MethodPost, "http://"+srv.Addr()+"/admin/signed-manifest", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer agent-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, respBody)
	}

	events, err := auditLog.Recent(5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ev := range events {
		if ev.Outcome == audit.OutcomeAccepted {
			found = true
			if ev.Actor != agentActor {
				t.Fatalf("actor=%q want %q", ev.Actor, agentActor)
			}
		}
	}
	if !found {
		t.Fatal("expected accepted upload audit event")
	}
}
