package server

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	paymentsv1 "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
	"io"
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
	return newSessionTestServerWithRunner(t, runner.handler()), runner
}

// newSessionTestServerWithRunner is newSessionTestServer with a
// caller-supplied runner, for the tests that need a descriptor the
// standard fake does not produce.
func newSessionTestServerWithRunner(t *testing.T, runnerHandler http.Handler) *httptest.Server {
	t.Helper()
	runnerSrv := httptest.NewServer(runnerHandler)
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
	return srv
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
	// An idempotent open converges on the usable outcome: the same
	// credential comes back, so a lost response is recoverable rather
	// than terminal for a session the gateway already funded.
	if replay["credential"] != open["credential"] {
		t.Fatalf("replay credential = %v; want the recorded %v", replay["credential"], open["credential"])
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

func TestTopUpResponseReturnsTheSuccessorWorkID(t *testing.T) {
	srv, _ := newSessionTestServer(t)

	// One wallet, two ticket identities — a rotation. Both payments must
	// carry the SAME sender or the rebind is refused for a mismatch the
	// scenario does not have.
	wallet := []byte("0123456789abcdef0123")
	pay := func(rand string) string {
		t.Helper()
		raw, err := proto.Marshal(&paymentsv1.Payment{
			Sender:       wallet,
			TicketParams: &paymentsv1.TicketParams{RecipientRandHash: []byte(rand)},
			// Must match the offering (10 wei per 1 unit) or the
			// envelope check refuses it. Opaque stub payments are
			// tolerated because they do not parse; a real one is held to
			// the price the sender signed.
			ExpectedPrice: &paymentsv1.PriceInfo{
				PricePerUnit:  10,
				PixelsPerUnit: 1,
				Constraint: "cap=livepeer:meet/sfu-room;off=default;wu=participant_minutes;" +
					"est=100;qid=q;qv=1;cfp=aa;rfp=bb",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return base64.StdEncoding.EncodeToString(raw)
	}

	openReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/session",
		strings.NewReader(`{"gateway_session_id":"gws-rebind","session_params":{}}`))
	openReq.Header.Set(livepeerheader.Capability, "livepeer:meet/sfu-room")
	openReq.Header.Set(livepeerheader.Offering, "default")
	openReq.Header.Set(livepeerheader.Protocol, "paid-session/v1")
	openReq.Header.Set(livepeerheader.RequestID, "req-topup-rebind")
	openReq.Header.Set(livepeerheader.Payment, pay("original-rand-0123456789abcdef0"))
	openResp, err := http.DefaultClient.Do(openReq)
	if err != nil {
		t.Fatal(err)
	}
	open := decode(t, openResp)
	sessionID := open["session_id"].(string)
	credential := open["credential"].(string)
	originalWorkID := open["work_id"].(string)

	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/session/"+sessionID+"/topup", nil)
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set(livepeerheader.RequestID, "topup-rebind-1")
	req.Header.Set(livepeerheader.RebindFrom, originalWorkID)
	req.Header.Set(livepeerheader.Payment, pay("successor-rand-0123456789abcdef"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("rebinding top-up status %d: %s", resp.StatusCode, body)
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	want := hex.EncodeToString([]byte("successor-rand-0123456789abcdef"))
	if got["work_id"] != want {
		t.Fatalf("work_id = %v; want the successor %s (returning %s sends the caller "+
			"back to the identity it just rotated away from)", got["work_id"], want, originalWorkID)
	}
}

// sessionOpenWithGatewayID opens a session declaring a specific
// gateway_session_id.
func sessionOpenWithGatewayID(t *testing.T, srv *httptest.Server, requestID, gatewayID string) *http.Response {
	t.Helper()
	body := `{"gateway_session_id":"` + gatewayID + `","session_params":{}}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/session", strings.NewReader(body))
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

// A clearinghouse holds only the id it issued itself, so the settlement
// query has to resolve it. session_id is broker-local and reaches LOC
// through the customer-controlled SDK — the channel the signature exists
// to distrust — and work_id can cover several sessions.
func TestSettlementQueryResolvesGatewaySessionID(t *testing.T) {
	srv, _ := newSessionTestServer(t)

	open := decode(t, sessionOpenWithGatewayID(t, srv, "req-gws-lookup", "loc-sess-9c21"))
	sessionID, _ := open["session_id"].(string)
	if sessionID == "" {
		t.Fatal("no session_id from open")
	}

	q, err := http.Get(srv.URL + "/v1/settlement/loc-sess-9c21")
	if err != nil {
		t.Fatal(err)
	}
	defer q.Body.Close()
	if q.StatusCode != http.StatusOK {
		t.Fatalf("query by gateway_session_id status %d; want 200", q.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(q.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if got, _ := body["session_id"].(string); got != sessionID {
		t.Fatalf("resolved to session %q; want %q", got, sessionID)
	}
	if got, _ := body["gateway_session_id"].(string); got != "loc-sess-9c21" {
		t.Fatalf("gateway_session_id = %q; want it echoed", got)
	}
}

// The lookup is only usable if the id resolves to one session, so a
// second open claiming it is refused rather than silently breaking the
// lookup for both.
func TestSessionOpenRefusesDuplicateGatewaySessionID(t *testing.T) {
	srv, _ := newSessionTestServer(t)

	first := sessionOpenWithGatewayID(t, srv, "req-dup-1", "loc-sess-dup")
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("first open status %d", first.StatusCode)
	}
	_ = first.Body.Close()

	second := sessionOpenWithGatewayID(t, srv, "req-dup-2", "loc-sess-dup")
	defer second.Body.Close()
	if second.StatusCode != http.StatusConflict {
		t.Fatalf("duplicate gateway_session_id status %d; want 409", second.StatusCode)
	}
	if got := second.Header.Get(livepeerheader.Error); got != livepeerheader.ErrGatewaySessionIDReuse {
		t.Fatalf("Livepeer-Error = %q; want %q", got, livepeerheader.ErrGatewaySessionIDReuse)
	}
}

// An omitted gateway_session_id must be refused, not accepted.
//
// The meeting team's client omitted it from the first day and nothing
// anywhere complained: sessions opened, work was served, and every
// settlement carried an empty value for the only identifier its consumer
// issues itself. Uniqueness was enforced only when the field was
// present, so the empty case reached — silently — exactly the failure
// the uniqueness rule exists to prevent.
func TestSessionOpenRequiresGatewaySessionID(t *testing.T) {
	srv, _ := newSessionTestServer(t)

	for _, tc := range []struct{ name, body string }{
		{"omitted", `{"session_params":{}}`},
		{"empty", `{"gateway_session_id":"","session_params":{}}`},
		{"whitespace", `{"gateway_session_id":"   ","session_params":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/session", strings.NewReader(tc.body))
			req.Header.Set(livepeerheader.Capability, "livepeer:meet/sfu-room")
			req.Header.Set(livepeerheader.Offering, "default")
			req.Header.Set(livepeerheader.Protocol, "paid-session/v1")
			req.Header.Set(livepeerheader.RequestID, "gws-required-"+tc.name)
			req.Header.Set(livepeerheader.Payment,
				base64.StdEncoding.EncodeToString([]byte("stub-payment")))
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("open without a gateway_session_id = %d; want 400 — accepting it "+
					"produces a settlement its consumer can never resolve", resp.StatusCode)
			}
		})
	}
}

// A descriptor with no grants must not emit `"grants": null`.
//
// The open response is assembled as a map[string]any, which has no
// omitempty, so a nil slice marshalled to null. The descriptor schemas
// require an array when the key is present, so a consumer validating the
// runtime block rejected an otherwise good open. Most descriptors carry
// grants, which is why this survived: the ones that do not were the
// broken case.
func TestSessionOpenNeverEmitsNullGrants(t *testing.T) {
	// A runner whose descriptor carries no grants at all.
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sessions", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"runner_session_id": "rns_nogrants",
			"runtime": {
				"schema": "sfu-room/v1",
				"public": {"url": "wss://sfu.example", "room": "rm_nogrants"}
			}
		}`)
	})
	mux.HandleFunc("GET /sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"runner_session_id":"rns_nogrants","state":"active"}`)
	})
	mux.HandleFunc("DELETE /sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := newSessionTestServerWithRunner(t, mux)

	resp := sessionOpenReq(t, srv, "sess-nogrants")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("open status %d: %s", resp.StatusCode, raw)
	}

	// Assert on the BYTES. Decoding to map[string]any turns both a null
	// and an absent key into a nil interface, so a decoded check cannot
	// tell the broken case from the fixed one.
	if bytes.Contains(raw, []byte(`"grants":null`)) ||
		bytes.Contains(raw, []byte(`"grants": null`)) {
		t.Fatalf("open emitted a null grants; the schema requires an array "+
			"when the key is present:\n%s", raw)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	rt, _ := out["runtime"].(map[string]any)
	if g, present := rt["grants"]; present {
		if _, isArray := g.([]any); !isArray {
			t.Fatalf("grants present but not an array: %T %v", g, g)
		}
	}
}
