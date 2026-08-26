package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/credentialstore"
)

func newCredentialTestServer(t *testing.T, withStore bool) *Server {
	t.Helper()
	t.Setenv("BROKER_ADMIN_TOKEN", "secret-token")
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "seal.key")
	if err := os.WriteFile(keyPath, []byte(strings.Repeat("ab", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	store := ""
	if withStore {
		store = "credential_store:\n  path: " + filepath.Join(dir, "creds.db") + "\n  sealing_key_file: " + keyPath + "\n"
	}
	configPath := filepath.Join(dir, "host-config.yaml")
	cfg := `
identity:
  orch_eth_address: 0x1234567890abcdef1234567890abcdef12345678
external_base_url: https://broker.example
admin_auth:
  method: bearer
  secret_ref: env://BROKER_ADMIN_TOKEN
listen:
  paid: ":8080"
  metrics: ":9090"
  worker_quic: ":8443"
payment_daemon:
  mock: true
` + store + `
capabilities:
  - id: rerank
    offering_id: shared
    protocol: paid-job/v1
    job: { transports: [unary] }
    work_unit: { name: requests, extractor: { type: request-formula, expression: "1" } }
    price: { amount_wei: "1", per_units: 1 }
    backend: { id: backend-a, transport: http, url: http://backend-a }
`
	if err := os.WriteFile(configPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := mustServerFromPath(t, configPath)
	return srv
}

func adminReq(t *testing.T, srv *Server, method, path, body string, hdr map[string]string) (int, map[string]any, http.Header) {
	t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rdr)
	req.Header.Set("Authorization", "Bearer secret-token")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	raw, _ := io.ReadAll(rec.Result().Body)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return rec.Code, out, rec.Header()
}

func TestCredentialAdminLifecycle(t *testing.T) {
	srv := newCredentialTestServer(t, true)

	// Enroll: token once, host id echoed, bundle shaped for the agent.
	code, res, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/enroll",
		`{"host_id":"host-1","label":"rig","expires_in_seconds":3600}`, map[string]string{"Livepeer-Request-Id": "enr-1"})
	if code != http.StatusCreated {
		t.Fatalf("enroll: %d %v", code, res)
	}
	cred := res["credential"].(map[string]any)
	token := cred["token"].(string)
	credID := res["credential_id"].(string)
	if !strings.HasPrefix(token, "lpc_") || cred["kind"] != "bearer" || res["host_id"] != "host-1" {
		t.Fatalf("enroll body: %v", res)
	}
	bundle := res["bundle"].(map[string]any)
	if bundle["broker_eth_address"] != "0x1234567890abcdef1234567890abcdef12345678" || bundle["contract_version"] != "1.0" {
		t.Fatalf("bundle: %v", bundle)
	}
	urls := bundle["broker_urls"].(map[string]any)
	if urls["ws"] != "wss://broker.example/internal/v1/worker/session" || urls["quic"] != ":8443" {
		t.Fatalf("bundle urls: %v", urls)
	}

	// Replay with the same request id returns the same token.
	code, res2, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/enroll",
		`{"host_id":"host-1","label":"rig","expires_in_seconds":3600}`, map[string]string{"Livepeer-Request-Id": "enr-1"})
	if code != http.StatusCreated || res2["credential"].(map[string]any)["token"] != token {
		t.Fatalf("replay: %d %v", code, res2)
	}
	// Without replay, the host is taken.
	code, res3, hdr := adminReq(t, srv, http.MethodPost, "/admin/v1/enroll", `{"host_id":"host-1"}`, nil)
	if code != http.StatusConflict || res3["code"] != "host_id_taken" || hdr.Get("Livepeer-Error") != "host_id_taken" {
		t.Fatalf("taken: %d %v", code, res3)
	}
	// Unknown field is rejected by name.
	code, res4, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/enroll", `{"host_id":"h2","price":"1"}`, nil)
	if code != http.StatusBadRequest || res4["code"] != "unknown_field" || !strings.Contains(res4["message"].(string), "price") {
		t.Fatalf("unknown field: %d %v", code, res4)
	}

	// The store authenticates the token for attach; listing hides it.
	if rec := srv.authenticateAttachCredential("Bearer " + token); rec == nil || rec.HostID != "host-1" {
		t.Fatalf("attach auth via store failed")
	}
	code, list, _ := adminReq(t, srv, http.MethodGet, "/admin/v1/credentials", "", nil)
	raw, _ := json.Marshal(list)
	if code != http.StatusOK || strings.Contains(string(raw), token) || strings.Contains(string(raw), "token_sha256") {
		t.Fatalf("list leaks: %d %s", code, raw)
	}
	code, one, _ := adminReq(t, srv, http.MethodGet, "/admin/v1/credentials/"+credID, "", nil)
	if code != http.StatusOK || one["state"] != "active" || one["host_id"] != "host-1" {
		t.Fatalf("get: %d %v", code, one)
	}

	// Rotate: new token, old still valid during grace.
	code, rot, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/credentials/"+credID+"/rotate", `{"grace_seconds":600}`, nil)
	if code != http.StatusCreated {
		t.Fatalf("rotate: %d %v", code, rot)
	}
	newToken := rot["credential"].(map[string]any)["token"].(string)
	if newToken == token || srv.authenticateAttachCredential("Bearer "+token) == nil || srv.authenticateAttachCredential("Bearer "+newToken) == nil {
		t.Fatal("rotation grace not honoured")
	}

	// Revoke kills tracked connections and ends both tokens.
	closer := &countingCloser{}
	untrack := srv.trackAttachedHost("Bearer "+newToken, closer)
	defer untrack()
	code, rev, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/credentials/"+credID+"/revoke", `{"reason":"lost"}`, nil)
	if code != http.StatusOK || rev["state"] != "revoked" || rev["connections_closed"] != float64(1) || closer.n != 1 {
		t.Fatalf("revoke: %d %v closed=%d", code, rev, closer.n)
	}
	for _, tok := range []string{token, newToken} {
		if srv.authenticateAttachCredential("Bearer "+tok) != nil {
			t.Fatal("revoked token still authenticates")
		}
	}
	code, _, _ = adminReq(t, srv, http.MethodPost, "/admin/v1/credentials/"+credID+"/rotate", `{}`, nil)
	if code != http.StatusConflict {
		t.Fatalf("rotate revoked: %d", code)
	}
	code, _, _ = adminReq(t, srv, http.MethodGet, "/admin/v1/credentials/cred_nope", "", nil)
	if code != http.StatusNotFound {
		t.Fatalf("get missing: %d", code)
	}
}

func TestCredentialSyncDropRevokes(t *testing.T) {
	srv := newCredentialTestServer(t, true)
	hash := credentialstore.HashToken
	body := `{"revision":"r1","credentials":[
	  {"credential_id":"cred_a","host_id":"host-a","kind":"bearer","token_sha256":"` + hash("lpc_a") + `","expires_at":"2099-01-01T00:00:00Z"},
	  {"credential_id":"cred_b","host_id":"host-b","kind":"bearer","token_sha256":"` + hash("lpc_b") + `","expires_at":"2099-01-01T00:00:00Z"}]}`
	code, res, _ := adminReq(t, srv, http.MethodPut, "/admin/v1/credentials", body, nil)
	if code != http.StatusOK || res["applied"] != true {
		t.Fatalf("sync: %d %v", code, res)
	}
	if srv.authenticateAttachCredential("Bearer lpc_b") == nil {
		t.Fatal("synced token does not authenticate")
	}
	closer := &countingCloser{}
	untrack := srv.trackAttachedHost("Bearer lpc_b", closer)
	defer untrack()
	body2 := `{"revision":"r2","credentials":[
	  {"credential_id":"cred_a","host_id":"host-a","kind":"bearer","token_sha256":"` + hash("lpc_a") + `","expires_at":"2099-01-01T00:00:00Z"}]}`
	code, res, _ = adminReq(t, srv, http.MethodPut, "/admin/v1/credentials", body2, nil)
	if code != http.StatusOK || res["connections_closed"] != float64(1) || closer.n != 1 {
		t.Fatalf("sync drop: %d %v", code, res)
	}
	if srv.authenticateAttachCredential("Bearer lpc_b") != nil {
		t.Fatal("dropped token still authenticates")
	}
	if srv.authenticateAttachCredential("Bearer lpc_a") == nil {
		t.Fatal("kept token stopped authenticating")
	}
}

func TestCredentialRoutesWithoutStore(t *testing.T) {
	srv := newCredentialTestServer(t, false)
	code, res, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/enroll", `{}`, nil)
	if code != http.StatusNotFound || res["code"] != "credential_store_disabled" {
		t.Fatalf("no store: %d %v", code, res)
	}
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/credentials", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized && rec.Code != http.StatusNotFound {
		t.Fatalf("unauthenticated: %d", rec.Code)
	}
}

type countingCloser struct{ n int }

func (c *countingCloser) Close() error { c.n++; return nil }

func mustServerFromPath(t *testing.T, configPath string) *Server {
	t.Helper()
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("config.Load() error = %v", err)
	}
	srv, err := New(cfg, Options{ConfigPath: configPath})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return srv
}
