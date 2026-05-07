package publicapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/published"
)

func newSrv(t *testing.T) (*Server, *published.Store) {
	t.Helper()
	dir := t.TempDir()
	store, err := published.New(filepath.Join(dir, "p"))
	if err != nil {
		t.Fatal(err)
	}
	srv := New("127.0.0.1:0", store, slog.Default())
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	return srv, store
}

func TestPublicAPI_503BeforePublish(t *testing.T) {
	srv, _ := newSrv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	resp, err := http.Get("http://" + srv.Addr() + WellKnownPath)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}
}

func TestPublicAPI_ServesPublishedBytes(t *testing.T) {
	srv, store := newSrv(t)
	if err := store.Lock(); err != nil {
		t.Fatal(err)
	}
	if err := store.Publish([]byte(`{"hello":"world"}`)); err != nil {
		t.Fatal(err)
	}
	store.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)

	resp, err := http.Get("http://" + srv.Addr() + WellKnownPath)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != `{"hello":"world"}` {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
}

func TestPublicAPI_404OnAllOtherPaths(t *testing.T) {
	srv, store := newSrv(t)
	store.Lock()
	store.Publish([]byte("x"))
	store.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)

	for _, path := range []string{
		"/",
		"/admin",
		"/admin/signed-manifest",
		"/candidate.json",
		"/healthz",
		"/.well-known/other",
		"/well-known/livepeer-registry.json",
	} {
		t.Run(path, func(t *testing.T) {
			resp, err := http.Get("http://" + srv.Addr() + path)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("path %q: expected 404, got %d", path, resp.StatusCode)
			}
		})
	}
}

func TestPublicAPI_RejectsNonGET(t *testing.T) {
	srv, store := newSrv(t)
	store.Lock()
	store.Publish([]byte("x"))
	store.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		t.Run(m, func(t *testing.T) {
			req, _ := http.NewRequest(m, "http://"+srv.Addr()+WellKnownPath, nil)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound {
				t.Fatalf("method %s: expected 404, got %d", m, resp.StatusCode)
			}
		})
	}
}
