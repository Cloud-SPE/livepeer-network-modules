package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
)

// runtimeAdminConfig is the smallest broker the runtime surface needs:
// one offer, and a credential store so a runner can attach and freeze
// it. Nothing here is advertised until a runner does.
func runtimeAdminConfig(dir, stateDir string) string {
	return `
identity:
  orch_eth_address: 0x1234567890abcdef1234567890abcdef12345678
admin_auth:
  method: bearer
  secret_ref: env://BROKER_ADMIN_TOKEN
listen:
  paid: ":8080"
  metrics: ":9090"
payment_daemon:
  mock: true
credential_store:
  path: ` + filepath.Join(dir, "creds.db") + `
  sealing_key_file: ` + filepath.Join(dir, "seal.key") + `
offers_state_path: ` + filepath.Join(stateDir, "offers-state.json") + `
offers:
  - offering_id: shared
    capability: text:rerank
    protocol: paid-job/v1
    price:
      amount_wei: "1"
      per_units: 1
`
}

// runtimeAdminDirs prepares the config directory plus the out-of-tree
// offers state directory (the attach handler persists it as a hijacked
// connection unwinds, which httptest.Server.Close does not wait for).
func runtimeAdminDirs(t *testing.T) (dir, stateDir string) {
	t.Helper()
	dir = t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "seal.key"),
		[]byte(strings.Repeat("ab", 32)), 0o600); err != nil {
		t.Fatalf("WriteFile(seal.key) error = %v", err)
	}
	stateDir, err := os.MkdirTemp("", "runtime-offers-state-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := 0; i < 50; i++ {
			if os.RemoveAll(stateDir) == nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	return dir, stateDir
}

func TestRuntimeStatusAndReload(t *testing.T) {
	if err := os.Setenv("BROKER_ADMIN_TOKEN", "secret-token"); err != nil {
		t.Fatalf("Setenv() error = %v", err)
	}
	defer os.Unsetenv("BROKER_ADMIN_TOKEN")
	dir, stateDir := runtimeAdminDirs(t)
	configPath := filepath.Join(dir, "host-config.yaml")
	initial := runtimeAdminConfig(dir, stateDir)
	if err := os.WriteFile(configPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	srv, err := New(cfg, Options{ConfigPath: configPath})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()
	// A runner has to be attached for anything to be advertised: the
	// offering below is only visible once a certified runner has frozen
	// a shape onto it.
	attachRuntimeAdminRunner(t, srv, ts)

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/runtime", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Result().Body)
	if rec.Code != http.StatusOK || !strings.Contains(string(body), `"loaded_revision":"`) || !strings.Contains(string(body), `"last_reload_status":"startup_loaded"`) || !strings.Contains(string(body), `"last_reload_attempt_id":"startup"`) || !strings.Contains(string(body), `"history":[`) {
		t.Fatalf("GET /admin/v1/runtime status=%d body=%s", rec.Code, string(body))
	}

	updated := strings.ReplaceAll(initial, "shared", "new-shared")
	if err := os.WriteFile(configPath, []byte(updated), 0o644); err != nil {
		t.Fatalf("WriteFile(updated) error = %v", err)
	}

	req = httptest.NewRequest(http.MethodPost, "/admin/v1/runtime/reload", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	body, _ = io.ReadAll(rec.Result().Body)
	if rec.Code != http.StatusOK || !strings.Contains(string(body), `"last_reload_status":"applied"`) || !strings.Contains(string(body), `"last_reload_attempt_id":"reload-`) || !strings.Contains(string(body), `"attempt_id":"reload-`) || !strings.Contains(string(body), `"status":"applied"`) {
		t.Fatalf("POST /admin/v1/runtime/reload status=%d body=%s", rec.Code, string(body))
	}

	// The reload re-matches the already-attached runner against the new
	// offer set, so the renamed offering re-freezes and is advertised.
	deadline := time.Now().Add(10 * time.Second)
	for {
		req = httptest.NewRequest(http.MethodGet, "/registry/offerings", nil)
		rec = httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, req)
		body, _ = io.ReadAll(rec.Result().Body)
		if rec.Code == http.StatusOK && strings.Contains(string(body), `"offering_id":"new-shared"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("GET /registry/offerings status=%d body=%s", rec.Code, string(body))
		}
		time.Sleep(20 * time.Millisecond)
	}

	// The worker-session admin surface is gone with the tunnel it
	// managed. It has to be gone from the mux too: a route that still
	// answers 200 is one a controller keeps calling, and this one
	// reported kills that never happened.
	for _, path := range []string{
		"/admin/v1/worker-sessions",
		"/admin/v1/worker-sessions/worker-backend-a/kill",
	} {
		req = httptest.NewRequest(http.MethodPost, path, nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		rec = httptest.NewRecorder()
		srv.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s status=%d, want 404 — the worker tunnel is deleted", path, rec.Code)
		}
	}
}

// attachRuntimeAdminRunner attaches one runner declaring the rerank
// capability and waits for the offer to freeze on its shape.
func attachRuntimeAdminRunner(t *testing.T, srv *Server, ts *httptest.Server) {
	t.Helper()
	_, enr, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/enroll", `{"host_id":"h1"}`, nil)
	token := enr["credential"].(map[string]any)["token"].(string)
	c := dialAttach(t, ts)
	res := register(t, c, attachDoc(token, "h1", func(m map[string]any) {
		m["capabilities"].([]any)[0].(map[string]any)["capability_id"] = "text:rerank"
	}))
	if res["document"] != "accepted" {
		t.Fatalf("attach: %v", res)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(srv.offersEngine.EligiblePairs("shared")) == 1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the attached runner never became eligible")
}

func TestRuntimeAdminRequiresAuth(t *testing.T) {
	if err := os.Setenv("BROKER_ADMIN_TOKEN", "secret-token"); err != nil {
		t.Fatalf("Setenv() error = %v", err)
	}
	defer os.Unsetenv("BROKER_ADMIN_TOKEN")
	dir, stateDir := runtimeAdminDirs(t)
	configPath := filepath.Join(dir, "host-config.yaml")
	if err := os.WriteFile(configPath, []byte(runtimeAdminConfig(dir, stateDir)), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	srv, err := New(cfg, Options{ConfigPath: configPath})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/runtime", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("GET /admin/v1/runtime without auth status=%d, want 401", rec.Code)
	}
}

func TestRuntimeReloadFailureIsRecordedInHistory(t *testing.T) {
	if err := os.Setenv("BROKER_ADMIN_TOKEN", "secret-token"); err != nil {
		t.Fatalf("Setenv() error = %v", err)
	}
	defer os.Unsetenv("BROKER_ADMIN_TOKEN")
	dir, stateDir := runtimeAdminDirs(t)
	configPath := filepath.Join(dir, "host-config.yaml")
	if err := os.WriteFile(configPath, []byte(runtimeAdminConfig(dir, stateDir)), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	srv, err := New(cfg, Options{ConfigPath: configPath})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if err := os.WriteFile(configPath, []byte("not: [valid"), 0o644); err != nil {
		t.Fatalf("WriteFile(invalid) error = %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/v1/runtime/reload", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Result().Body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /admin/v1/runtime/reload status=%d body=%s", rec.Code, string(body))
	}

	req = httptest.NewRequest(http.MethodGet, "/admin/v1/runtime", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	body, _ = io.ReadAll(rec.Result().Body)
	if rec.Code != http.StatusOK || !strings.Contains(string(body), `"last_reload_status":"failed"`) || !strings.Contains(string(body), `"last_reload_attempt_id":"reload-`) || !strings.Contains(string(body), `"attempt_id":"reload-`) || !strings.Contains(string(body), `"status":"failed"`) {
		t.Fatalf("GET /admin/v1/runtime after failure status=%d body=%s", rec.Code, string(body))
	}
}
