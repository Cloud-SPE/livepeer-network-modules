package server

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/health"
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
    protocol: paid-job/v1
    job:
      transports: [unary]
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

	req = httptest.NewRequest(http.MethodGet, "/registry/offerings", nil)
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	body, _ = io.ReadAll(rec.Result().Body)
	if rec.Code != http.StatusOK || !strings.Contains(string(body), `"offering_id":"new-shared"`) {
		t.Fatalf("GET /registry/offerings status=%d body=%s", rec.Code, string(body))
	}

	if err := srv.workerRegistry.Register("worker-backend-a", runtimeAdminStubForwarder{}); err != nil {
		t.Fatalf("worker registry Register() error = %v", err)
	}
	req = httptest.NewRequest(http.MethodGet, "/admin/v1/worker-sessions", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	body, _ = io.ReadAll(rec.Result().Body)
	if rec.Code != http.StatusOK || !strings.Contains(string(body), `"worker-backend-a"`) {
		t.Fatalf("GET /admin/v1/worker-sessions status=%d body=%s", rec.Code, string(body))
	}

	req = httptest.NewRequest(http.MethodPost, "/admin/v1/worker-sessions/worker-backend-a/kill", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec = httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	body, _ = io.ReadAll(rec.Result().Body)
	if rec.Code != http.StatusOK || !strings.Contains(string(body), `"status":"killed"`) {
		t.Fatalf("POST /admin/v1/worker-sessions/{id}/kill status=%d body=%s", rec.Code, string(body))
	}
}

type runtimeAdminStubForwarder struct{}

func (runtimeAdminStubForwarder) Forward(context.Context, backend.ForwardRequest) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}, nil
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
    protocol: paid-job/v1
    job:
      transports: [unary]
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

func TestRuntimeReloadPreservesHealthSnapshotState(t *testing.T) {
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
    protocol: paid-job/v1
    job:
      transports: [unary]
    work_unit:
      name: requests
      extractor:
        type: request-formula
        expression: "1"
    health:
      probe:
        type: http-status
        interval_ms: 5000
        timeout_ms: 1500
        unhealthy_after: 1
        healthy_after: 1
        config:
          url: http://backend-a/healthz
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
	srv.health = health.NewWithSnapshots(cfg, []health.Snapshot{{
		ID:                   "rerank",
		OfferingID:           "shared",
		BackendID:            "backend-a",
		Status:               health.StatusReady,
		Reason:               "probe_ok",
		ProbeType:            "http-status",
		ConsecutiveSuccesses: 4,
	}})

	req := httptest.NewRequest(http.MethodPost, "/admin/v1/runtime/reload", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	body, _ := io.ReadAll(rec.Result().Body)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /admin/v1/runtime/reload status=%d body=%s", rec.Code, string(body))
	}

	snap := srv.currentHealth().Snapshot()
	if len(snap.Capabilities) != 1 {
		t.Fatalf("capability count = %d, want 1", len(snap.Capabilities))
	}
	if snap.Capabilities[0].Status != health.StatusReady || snap.Capabilities[0].ConsecutiveSuccesses != 4 {
		t.Fatalf("health snapshot = %#v", snap.Capabilities[0])
	}
}

func TestRuntimeReloadFailureIsRecordedInHistory(t *testing.T) {
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
    protocol: paid-job/v1
    job:
      transports: [unary]
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
