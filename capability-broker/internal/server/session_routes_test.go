package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
)

// End-to-end paid-session surface test: open → status → events → end
// over real HTTP against a fake runner, with the mock payment daemon.

type fakeSessionRunner struct {
	mu            sync.Mutex
	callbackToken string
	callbackURL   string
	terminates    int
}

func (f *fakeSessionRunner) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sessions", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			CallbackURL   string `json:"callback_url"`
			CallbackToken string `json:"callback_token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.callbackToken = req.CallbackToken
		f.callbackURL = req.CallbackURL
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"runner_session_id": "rns_e2e",
			"runtime": {
				"schema": "sfu-room/v1",
				"public": {"url": "wss://sfu.example", "room": "rm_e2e"},
				"private": {"terminate_token": "rt_hidden"},
				"grants": [{"id":"g1","operations":["participant-token-mint"],"secret":"gs_hidden","expires_at":"2030-01-01T00:00:00Z"}]
			}
		}`)
	})
	mux.HandleFunc("GET /sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"runner_session_id":"rns_e2e","state":"active"}`)
	})
	mux.HandleFunc("DELETE /sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.terminates++
		f.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func newSessionTestServer(t *testing.T) (*httptest.Server, *fakeSessionRunner) {
	t.Helper()
	runner := &fakeSessionRunner{}
	runnerSrv := httptest.NewServer(runner.handler())
	t.Cleanup(runnerSrv.Close)

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "seal.key")
	if err := os.WriteFile(keyPath, make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Identity:        config.Identity{OrchEthAddress: "0x" + strings.Repeat("ab", 20)},
		ExternalBaseURL: "https://broker.example.com",
		PaymentDaemon:   config.PaymentDaemon{Mock: true},
		SessionStore: config.SessionStore{
			Path:           filepath.Join(dir, "sessions.db"),
			SealingKeyFile: keyPath,
		},
		Capabilities: []config.Capability{{
			ID:         "livepeer:meet/sfu-room",
			OfferingID: "default",
			Protocol:   "paid-session/v1",
			Session: &config.SessionCap{
				DescriptorSchema: "sfu-room/v1",
				Runner: config.SessionRunnerPaths{
					CreatePath:    "/sessions",
					StatusPath:    "/sessions/{id}",
					TerminatePath: "/sessions/{id}",
				},
			},
			WorkUnit: config.WorkUnit{Name: "participant_minutes"},
			Price:    config.Price{AmountWei: "10", PerUnits: 1},
			Backend:  config.Backend{Transport: "http", URL: runnerSrv.URL},
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}
	s, err := New(cfg, Options{})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	t.Cleanup(func() {
		if s.sessionStore != nil {
			_ = s.sessionStore.Close()
		}
	})
	srv := httptest.NewServer(s.mux)
	t.Cleanup(srv.Close)
	return srv, runner
}

func sessionOpenReq(t *testing.T, srv *httptest.Server, requestID string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/session",
		strings.NewReader(`{"gateway_session_id":"gws-1","session_params":{"room_hint":"standup"}}`))
	req.Header.Set(livepeerheader.Capability, "livepeer:meet/sfu-room")
	req.Header.Set(livepeerheader.Offering, "default")
	req.Header.Set(livepeerheader.Protocol, "paid-session/v1")
	req.Header.Set(livepeerheader.RequestID, requestID)
	req.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString([]byte("stub-payment")))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

func TestSessionSurfaceEndToEnd(t *testing.T) {
	srv, runner := newSessionTestServer(t)

	// --- open
	resp := sessionOpenReq(t, srv, "req-e2e-1")
	if resp.StatusCode != http.StatusCreated {
		body := decode(t, resp)
		t.Fatalf("open status %d: %v", resp.StatusCode, body)
	}
	open := decode(t, resp)
	sessionID := open["session_id"].(string)
	credential := open["credential"].(string)
	runtime := open["runtime"].(map[string]any)
	if runtime["schema"] != "sfu-room/v1" {
		t.Fatalf("schema: %v", runtime["schema"])
	}
	if grants := runtime["grants"].([]any); len(grants) != 1 {
		t.Fatalf("grants: %v", grants)
	}
	if strings.Contains(fmt.Sprint(open), "rt_hidden") {
		t.Fatal("private descriptor material leaked in open response")
	}
	if runner.callbackToken == "" || !strings.HasPrefix(runner.callbackURL, "https://broker.example.com/v1/session/") {
		t.Fatalf("callback coordinates wrong: %q %q", runner.callbackToken, runner.callbackURL)
	}

	// --- idempotent replay: same session, no credential, no grants
	replay := decode(t, sessionOpenReq(t, srv, "req-e2e-1"))
	if replay["session_id"] != sessionID {
		t.Fatal("replay minted a different session")
	}
	if _, has := replay["credential"]; has {
		t.Fatal("replay re-delivered the credential")
	}
	if g, ok := replay["runtime"].(map[string]any)["grants"]; ok && g != nil {
		if arr, isArr := g.([]any); isArr && len(arr) > 0 {
			t.Fatal("replay re-delivered grants")
		}
	}

	// --- status: credential-authenticated, identical public, no grants
	get := func(cred string) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/session/"+sessionID, nil)
		req.Header.Set("Authorization", "Bearer "+cred)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	stResp := get(credential)
	if stResp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", stResp.StatusCode)
	}
	st := decode(t, stResp)
	pubOpen, _ := json.Marshal(runtime["public"])
	pubStatus, _ := json.Marshal(st["runtime"].(map[string]any)["public"])
	if string(pubOpen) != string(pubStatus) {
		t.Fatalf("open/status public mismatch: %s vs %s", pubOpen, pubStatus)
	}
	if _, has := st["runtime"].(map[string]any)["grants"]; has {
		t.Fatal("status returned grants")
	}

	// --- uniform 401: bad credential vs unknown session identical
	bad := get("sc_wrong")
	req2, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/session/sess_unknown", nil)
	req2.Header.Set("Authorization", "Bearer "+credential)
	unknown, _ := http.DefaultClient.Do(req2)
	if bad.StatusCode != http.StatusUnauthorized || unknown.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected uniform 401s: %d %d", bad.StatusCode, unknown.StatusCode)
	}
	badBody := decode(t, bad)
	unknownBody := decode(t, unknown)
	if fmt.Sprint(badBody) != fmt.Sprint(unknownBody) {
		t.Fatalf("401 bodies distinguishable: %v vs %v", badBody, unknownBody)
	}

	// --- runner event with the captured callback token
	postEvent := func(token, body string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/session/"+sessionID+"/events",
			strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	evResp := postEvent(runner.callbackToken,
		`{"event_id":"evt_1","sequence":1,"event_type":"session.usage.tick","usage":{"unit":"participant_minutes","total":7}}`)
	if evResp.StatusCode != http.StatusOK {
		t.Fatalf("event: %d %v", evResp.StatusCode, decode(t, evResp))
	}
	evResp.Body.Close()
	// Bad token on events: uniform 401.
	if r := postEvent("cb_wrong", `{"event_id":"evt_2","sequence":2,"event_type":"session.heartbeat"}`); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad-token event status %d", r.StatusCode)
	}
	// Unit mismatch: 400, nothing advanced.
	if r := postEvent(runner.callbackToken,
		`{"event_id":"evt_3","sequence":2,"event_type":"session.usage.tick","usage":{"unit":"frames","total":9}}`); r.StatusCode != http.StatusBadRequest {
		t.Fatalf("unit mismatch status %d", r.StatusCode)
	}
	st2 := decode(t, get(credential))
	if st2["usage"].(map[string]any)["claimed_total"].(float64) != 7 {
		t.Fatalf("claimed total: %v", st2["usage"])
	}

	// --- end: idempotent, terminal
	end := func() map[string]any {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/session/"+sessionID+"/end",
			strings.NewReader(`{"reason":"gateway_close"}`))
		req.Header.Set("Authorization", "Bearer "+credential)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("end: %d", resp.StatusCode)
		}
		return decode(t, resp)
	}
	first := end()
	second := end()
	if first["close_reason"] != "gateway_close" || second["ended_at"] != first["ended_at"] {
		t.Fatalf("end not idempotent: %v vs %v", first, second)
	}
	if runner.terminates == 0 {
		t.Fatal("runner never terminated")
	}
}

// A runner that contradicts its configuration must cost that capability
// only — not the whole broker, and not the other capabilities an
// operator serves (paid-session §7.1.1).
func TestQuarantinedCapabilityIsWithheldNotFatal(t *testing.T) {
	// A runner whose describe path declares a different work unit.
	runner := &fakeSessionRunner{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /describe", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"protocols":["paid-session/v1"],"capabilities":[
			{"capability_id":"livepeer:meet/sfu-room","descriptor_schemas":["sfu-room/v1"],
			 "work_unit":"participant_minutes"}]}`)
	})
	mux.Handle("/", runner.handler())
	runnerSrv := httptest.NewServer(mux)
	t.Cleanup(runnerSrv.Close)

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "seal.key")
	if err := os.WriteFile(keyPath, make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	sessionCap := func(offering, unit string, describe string) config.Capability {
		return config.Capability{
			ID: "livepeer:meet/sfu-room", OfferingID: offering, Protocol: "paid-session/v1",
			Session: &config.SessionCap{
				DescriptorSchema: "sfu-room/v1",
				Runner: config.SessionRunnerPaths{
					CreatePath: "/sessions", StatusPath: "/sessions/{id}",
					TerminatePath: "/sessions/{id}", DescribePath: describe,
				},
			},
			Health:   config.Health{InitialStatus: "ready"},
			WorkUnit: config.WorkUnit{Name: unit},
			Price:    config.Price{AmountWei: "10", PerUnits: 1},
			Backend:  config.Backend{Transport: "http", URL: runnerSrv.URL},
		}
	}
	cfg := &config.Config{
		Identity:        config.Identity{OrchEthAddress: "0x" + strings.Repeat("ef", 20)},
		ExternalBaseURL: "https://broker.example.com",
		PaymentDaemon:   config.PaymentDaemon{Mock: true},
		SessionStore:    config.SessionStore{Path: filepath.Join(dir, "s.db"), SealingKeyFile: keyPath},
		Capabilities: []config.Capability{
			// Contradicted: config says seconds, runner says minutes.
			sessionCap("contradicted", "participant_seconds", "/describe"),
			// Healthy sibling with no describe path.
			sessionCap("healthy", "participant_seconds", ""),
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}

	// The broker must still start.
	s, err := New(cfg, Options{})
	if err != nil {
		t.Fatalf("broker refused to start over one bad capability: %v", err)
	}
	t.Cleanup(func() {
		if s.sessionStore != nil {
			_ = s.sessionStore.Close()
		}
	})
	if !s.isQuarantined("livepeer:meet/sfu-room", "contradicted") {
		t.Fatal("contradicted capability was not quarantined")
	}
	if s.isQuarantined("livepeer:meet/sfu-room", "healthy") {
		t.Fatal("healthy sibling was quarantined")
	}
	if reason := s.quarantineReasons()["livepeer:meet/sfu-room|contradicted"]; !strings.Contains(reason, "work_unit") {
		t.Fatalf("quarantine reason does not name the field: %q", reason)
	}

	// It must also disappear from discovery — advertising it would route
	// paid work to something that cannot serve it.
	srv := httptest.NewServer(s.mux)
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + "/registry/offerings")
	if err != nil {
		t.Fatal(err)
	}
	body := decode(t, resp)
	caps, _ := body["capabilities"].([]any)
	for _, c := range caps {
		if m, ok := c.(map[string]any); ok && m["offering_id"] == "contradicted" {
			t.Fatal("quarantined capability is still advertised")
		}
	}
	if len(caps) != 1 {
		t.Fatalf("expected the healthy sibling to remain advertised, got %d", len(caps))
	}
}

// A runner that declares its own readiness endpoint saves the operator
// from authoring a probe recipe for a fact the runner holds exactly.
// The derivation must land before the health manager is built.
func TestReadinessProbeDerivedFromRunnerDeclaration(t *testing.T) {
	runner := &fakeSessionRunner{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /describe", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"protocols":["paid-session/v1"],"capabilities":[
			{"capability_id":"livepeer:meet/sfu-room","descriptor_schemas":["sfu-room/v1"],
			 "work_unit":"participant_minutes","readiness":{"path":"/ready"}}]}`)
	})
	mux.Handle("/", runner.handler())
	runnerSrv := httptest.NewServer(mux)
	t.Cleanup(runnerSrv.Close)

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "seal.key")
	if err := os.WriteFile(keyPath, make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Identity:        config.Identity{OrchEthAddress: "0x" + strings.Repeat("ba", 20)},
		ExternalBaseURL: "https://broker.example.com",
		PaymentDaemon:   config.PaymentDaemon{Mock: true},
		SessionStore:    config.SessionStore{Path: filepath.Join(dir, "s.db"), SealingKeyFile: keyPath},
		Capabilities: []config.Capability{{
			ID: "livepeer:meet/sfu-room", OfferingID: "default", Protocol: "paid-session/v1",
			Session: &config.SessionCap{
				DescriptorSchema: "sfu-room/v1",
				Runner: config.SessionRunnerPaths{
					CreatePath: "/sessions", StatusPath: "/sessions/{id}",
					TerminatePath: "/sessions/{id}", DescribePath: "/describe",
				},
			},
			WorkUnit: config.WorkUnit{Name: "participant_minutes"},
			Price:    config.Price{AmountWei: "10", PerUnits: 1},
			Backend:  config.Backend{Transport: "http", URL: runnerSrv.URL},
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}
	s, err := New(cfg, Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if s.sessionStore != nil {
			_ = s.sessionStore.Close()
		}
	})
	got := s.currentConfig().Capabilities[0].Health.Probe
	if got.Type != "http-status" {
		t.Fatalf("probe type %q, want http-status", got.Type)
	}
	if u, _ := got.Config["url"].(string); u != runnerSrv.URL+"/ready" {
		t.Fatalf("probe url %q, want the runner's declared readiness endpoint %q", u, runnerSrv.URL+"/ready")
	}

	// An operator-written probe must win over the runner's declaration.
	cfg2 := *cfg
	caps := make([]config.Capability, 1)
	caps[0] = cfg.Capabilities[0]
	caps[0].Health = config.Health{Probe: config.HealthProbe{
		Type:   "http-status",
		Config: map[string]any{"url": runnerSrv.URL + "/operator-choice"},
	}}
	cfg2.Capabilities = caps
	cfg2.SessionStore = config.SessionStore{
		Path: filepath.Join(t.TempDir(), "s2.db"), SealingKeyFile: keyPath,
	}
	if err := cfg2.Validate(); err != nil {
		t.Fatalf("config2: %v", err)
	}
	s2, err := New(&cfg2, Options{})
	if err != nil {
		t.Fatalf("New(2): %v", err)
	}
	t.Cleanup(func() {
		if s2.sessionStore != nil {
			_ = s2.sessionStore.Close()
		}
	})
	if u, _ := s2.currentConfig().Capabilities[0].Health.Probe.Config["url"].(string); u != runnerSrv.URL+"/operator-choice" {
		t.Fatalf("operator probe overridden by the runner: got %q", u)
	}
}
