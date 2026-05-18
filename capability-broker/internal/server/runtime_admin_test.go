package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
)

func TestRuntimeStatusAndReload(t *testing.T) {
	if err := os.Setenv("BROKER_ADMIN_TOKEN", "secret-token"); err != nil {
		t.Fatalf("Setenv() error = %v", err)
	}
	defer os.Unsetenv("BROKER_ADMIN_TOKEN")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "host-config.yaml")
	initial := `
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
capabilities:
  - id: rerank
    offering_id: shared
    interaction_mode: http-reqresp@v0
    work_unit:
      name: requests
      extractor:
        type: request-formula
        expression: "1"
    price:
      amount_wei: "1"
      per_units: 1
    backend:
      id: backend-a
      transport: http
      url: http://backend-a
`
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

	req := httptest.NewRequest(http.MethodGet, "/admin/v1/runtime", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Result().Body)
	if rec.Code != http.StatusOK || !strings.Contains(string(body), `"loaded_revision":"`) || !strings.Contains(string(body), `"last_reload_status":"startup_loaded"`) {
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
	if rec.Code != http.StatusOK || !strings.Contains(string(body), `"last_reload_status":"applied"`) {
		t.Fatalf("POST /admin/v1/runtime/reload status=%d body=%s", rec.Code, string(body))
	}

	req = httptest.NewRequest(http.MethodGet, "/registry/offerings", nil)
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	body, _ = io.ReadAll(rec.Result().Body)
	if rec.Code != http.StatusOK || !strings.Contains(string(body), `"offering_id":"new-shared"`) {
		t.Fatalf("GET /registry/offerings status=%d body=%s", rec.Code, string(body))
	}
}

func TestRuntimeAdminRequiresAuth(t *testing.T) {
	if err := os.Setenv("BROKER_ADMIN_TOKEN", "secret-token"); err != nil {
		t.Fatalf("Setenv() error = %v", err)
	}
	defer os.Unsetenv("BROKER_ADMIN_TOKEN")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "host-config.yaml")
	raw := `
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
capabilities:
  - id: rerank
    offering_id: shared
    interaction_mode: http-reqresp@v0
    work_unit:
      name: requests
      extractor:
        type: request-formula
        expression: "1"
    price:
      amount_wei: "1"
      per_units: 1
    backend:
      id: backend-a
      transport: http
      url: http://backend-a
`
	if err := os.WriteFile(configPath, []byte(raw), 0o644); err != nil {
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
