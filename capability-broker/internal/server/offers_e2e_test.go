package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newOffersTestServer: credential store + one offer selecting
// identity.openai.model=llama, empty certification (certify on match).
func newOffersTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("BROKER_ADMIN_TOKEN", "secret-token")
	dir := t.TempDir()
	// The offers state file is written by the attach handler when a
	// hijacked WS connection unwinds — which httptest.Server.Close does
	// not wait for. Keep it outside t.TempDir and clean up with retries
	// so the write cannot race RemoveAll.
	stateDir, err := os.MkdirTemp("", "offers-state-*")
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
	keyPath := filepath.Join(dir, "seal.key")
	if err := os.WriteFile(keyPath, []byte(strings.Repeat("ab", 32)), 0o600); err != nil {
		t.Fatal(err)
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
payment_daemon:
  mock: true
credential_store:
  path: ` + filepath.Join(dir, "creds.db") + `
  sealing_key_file: ` + keyPath + `
offers_state_path: ` + filepath.Join(stateDir, "offers-state.json") + `
offers:
  - offering_id: llama-shared
    capability: openai:chat-completions
    protocol: paid-job/v1
    match: { identity.openai.model: llama }
    price: { amount_wei: "210", per_units: 1 }
    # extra.openai/provider are required of an openai:* offer by
    # config validation even though the runner's identity supplies
    # the authoritative values at freeze.
    extra: { region: us-west-2, provider: vllm, openai: { model: llama } }
    extra_from_runner: [x-quant]
`
	if err := os.WriteFile(configPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return mustServerFromPath(t, configPath)
}

func offeringsPayloadOf(t *testing.T, srv *Server) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/registry/offerings", nil)
	rec := httptest.NewRecorder()
	srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("offerings: %d", rec.Code)
	}
	out := map[string]any{}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

func TestOfferFreezeAdvertiseAcceptShape(t *testing.T) {
	srv := newOffersTestServer(t)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	// Before any runner: spec_version stamped, no capabilities.
	payload := offeringsPayloadOf(t, srv)
	if payload["spec_version"] != "2.4.1" {
		t.Fatalf("spec_version: %v", payload["spec_version"])
	}
	if n := len(payload["capabilities"].([]any)); n != 0 {
		t.Fatalf("unfrozen offer advertised: %d tuples", n)
	}

	// Enroll + attach a matching runner: certify-on-match freezes.
	_, enr, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/enroll", `{"host_id":"h1"}`, nil)
	token := enr["credential"].(map[string]any)["token"].(string)
	c := dialAttach(t, ts)
	res := register(t, c, attachDoc(token, "h1", func(m map[string]any) {
		cap0 := m["capabilities"].([]any)[0].(map[string]any)
		cap0["identity"] = map[string]any{"openai.model": "llama", "provider": "vllm"}
		cap0["x-quant"] = "fp8"
	}))
	if res["document"] != "accepted" {
		t.Fatalf("attach: %v", res)
	}

	payload = offeringsPayloadOf(t, srv)
	caps := payload["capabilities"].([]any)
	if len(caps) != 1 {
		t.Fatalf("tuples after freeze: %d", len(caps))
	}
	tup := caps[0].(map[string]any)
	if tup["offering_id"] != "llama-shared" || tup["price_per_unit_wei"] != "210" {
		t.Fatalf("tuple: %v", tup)
	}
	extra := tup["extra"].(map[string]any)
	if extra["region"] != "us-west-2" || extra["openai"].(map[string]any)["model"] != "llama" ||
		extra["provider"] != "vllm" || extra["x-quant"] != "fp8" {
		t.Fatalf("extra: %v", extra)
	}
	if tup["work_unit"].(map[string]any)["name"] != "tokens" {
		t.Fatalf("work_unit: %v", tup["work_unit"])
	}
	frozenBytes, _ := json.Marshal(payload["capabilities"])

	// Admin view: frozen, advertised, counts.
	_, ov, _ := adminReq(t, srv, http.MethodGet, "/admin/v1/offers/llama-shared", "", nil)
	if ov["state"] != "frozen" || ov["advertised"] != true || ov["frozen"].(map[string]any)["frozen_by"].(map[string]any)["host_id"] != "h1" {
		t.Fatalf("offer view: %v", ov)
	}

	// A mismatching runner (different promoted value): ineligible, offer
	// byte-identical, candidate visible; runner view names the field.
	_, enr2, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/enroll", `{"host_id":"h2"}`, nil)
	token2 := enr2["credential"].(map[string]any)["token"].(string)
	c2 := dialAttach(t, ts)
	res = register(t, c2, attachDoc(token2, "h2", func(m map[string]any) {
		cap0 := m["capabilities"].([]any)[0].(map[string]any)
		cap0["identity"] = map[string]any{"openai.model": "llama", "provider": "vllm"}
		cap0["x-quant"] = "int8"
	}))
	if res["document"] != "accepted" {
		t.Fatalf("attach 2: %v", res)
	}
	payload = offeringsPayloadOf(t, srv)
	nowBytes, _ := json.Marshal(payload["capabilities"])
	if string(nowBytes) != string(frozenBytes) {
		t.Fatalf("offerings changed on runner churn:\n%s\nvs\n%s", frozenBytes, nowBytes)
	}
	_, ov, _ = adminReq(t, srv, http.MethodGet, "/admin/v1/offers/llama-shared", "", nil)
	runners := ov["runners"].(map[string]any)
	cands := ov["candidates"].([]any)
	if runners["eligible"] != float64(1) || runners["ineligible"] != float64(1) || len(cands) != 1 {
		t.Fatalf("counts/candidates: %v %v", runners, cands)
	}
	_, rv, _ := adminReq(t, srv, http.MethodGet, "/admin/v1/runners/h2", "", nil)
	pair := rv["capabilities"].([]any)[0].(map[string]any)["offers"].([]any)[0].(map[string]any)
	if pair["state"] != "ineligible" || pair["reason"].(map[string]any)["field"] != "/promoted/x-quant" {
		t.Fatalf("pair: %v", pair)
	}

	// accept-shape on the candidate hash; offerings now carry the new
	// shape; eligibility flipped.
	hash := cands[0].(map[string]any)["shape_hash"].(string)
	code, acc, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/offers/llama-shared/accept-shape",
		`{"shape_hash":"`+hash+`"}`, nil)
	if code != http.StatusAccepted || acc["eligible_now"] != float64(1) || acc["ineligible_now"] != float64(1) {
		t.Fatalf("accept-shape: %d %v", code, acc)
	}
	payload = offeringsPayloadOf(t, srv)
	extra = payload["capabilities"].([]any)[0].(map[string]any)["extra"].(map[string]any)
	if extra["x-quant"] != "int8" {
		t.Fatalf("advertised after accept: %v", extra)
	}
	// Wrong hash → shape_not_candidate.
	code, bad, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/offers/llama-shared/accept-shape",
		`{"shape_hash":"sha256:nope"}`, nil)
	if code != http.StatusConflict || bad["code"] != "shape_not_candidate" {
		t.Fatalf("bad accept: %d %v", code, bad)
	}
	// Confirm publish → frozen on the new shape.
	code, conf, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/offers/llama-shared/confirm-published",
		`{"shape_hash":"`+hash+`"}`, nil)
	if code != http.StatusOK || conf["state"] != "frozen" {
		t.Fatalf("confirm: %d %v", code, conf)
	}

	// Disable → not advertised; enable → back.
	code, _, _ = adminReq(t, srv, http.MethodPost, "/admin/v1/offers/llama-shared/disable", "", nil)
	if code != http.StatusOK {
		t.Fatalf("disable: %d", code)
	}
	if n := len(offeringsPayloadOf(t, srv)["capabilities"].([]any)); n != 0 {
		t.Fatalf("disabled offer advertised: %d", n)
	}
	code, _, _ = adminReq(t, srv, http.MethodPost, "/admin/v1/offers/llama-shared/enable", "", nil)
	if code != http.StatusOK || len(offeringsPayloadOf(t, srv)["capabilities"].([]any)) != 1 {
		t.Fatal("enable did not restore advertisement")
	}
}

func TestOffersPutValidatesAndRefusesOnFileSource(t *testing.T) {
	// A file-sourced broker answers 409 before looking at the body
	// (broker-admin §4.2: the source conflict precedes validation).
	srv := newOffersTestServer(t)
	code, res, _ := adminReq(t, srv, http.MethodPut, "/admin/v1/offers",
		`{"revision":"r1","offers":[]}`, nil)
	if code != http.StatusConflict || res["code"] != "offers_source_is_file" {
		t.Fatalf("file source put: %d %v", code, res)
	}

	// An admin-sourced broker validates the push grammar in full before
	// anything changes.
	srvAdmin := newAdminSourcedTestServer(t)
	code, res, _ = adminReq(t, srvAdmin, http.MethodPut, "/admin/v1/offers",
		`{"revision":"r1","offers":[{"offering_id":"x","capability":"c","protocol":"nope/v1","price":{"amount_wei":"1","per_units":1}}]}`, nil)
	if code != http.StatusBadRequest || res["code"] != "offer_invalid" {
		t.Fatalf("invalid push: %d %v", code, res)
	}
	// A valid push applies and is reported on the offerings payload.
	code, res, _ = adminReq(t, srvAdmin, http.MethodPut, "/admin/v1/offers",
		`{"revision":"r2","offers":[{"offering_id":"pushed","capability":"openai:chat-completions","protocol":"paid-job/v1","price":{"amount_wei":"1","per_units":1}}]}`, nil)
	if code != http.StatusOK || res["applied"] != true || res["changed"].([]any)[0] != "pushed" {
		t.Fatalf("valid push: %d %v", code, res)
	}
	if offeringsPayloadOf(t, srvAdmin)["offers_revision"] != "r2" {
		t.Fatal("offers_revision not reported")
	}
}

// newAdminSourcedTestServer: offers_source admin, empty file offers[].
func newAdminSourcedTestServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("BROKER_ADMIN_TOKEN", "secret-token")
	dir := t.TempDir()
	configPath := filepath.Join(dir, "host-config.yaml")
	cfg := `
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
offers_source: admin
offers_state_path: ` + filepath.Join(dir, "offers-state.json") + `
`
	if err := os.WriteFile(configPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return mustServerFromPath(t, configPath)
}
