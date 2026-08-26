package server

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/workerconn"
	"github.com/gorilla/websocket"
)

// runnerSide is the single reader on the runner's websocket, like the
// agent: it answers tunnel request frames via the in-test handler and
// relays register_result frames to the returned channel.
func runnerSide(t *testing.T, c *websocket.Conn, handler func(method, path string, headers map[string][]string, body []byte) (int, string, []byte)) <-chan workerconn.TunnelMessage {
	t.Helper()
	results := make(chan workerconn.TunnelMessage, 4)
	go func() {
		defer close(results)
		for {
			var msg workerconn.TunnelMessage
			if err := c.ReadJSON(&msg); err != nil {
				return
			}
			switch msg.Type {
			case workerconn.MessageTypeRegisterResult:
				results <- msg
			case workerconn.MessageTypeRequest:
				body, _ := base64.StdEncoding.DecodeString(msg.BodyBase64)
				status, contentType, respBody := handler(msg.Method, msg.URL, msg.Headers, body)
				reply := workerconn.TunnelMessage{
					Type: workerconn.MessageTypeResponse, ID: msg.ID, StatusCode: status,
					Headers:    map[string][]string{"Content-Type": {contentType}},
					BodyBase64: base64.StdEncoding.EncodeToString(respBody),
				}
				if err := c.WriteJSON(reply); err != nil {
					return
				}
			}
		}
	}()
	return results
}

// registerVia writes a register frame and waits for the result relayed
// by runnerSide.
func registerVia(t *testing.T, c *websocket.Conn, results <-chan workerconn.TunnelMessage, doc []byte) map[string]any {
	t.Helper()
	if err := c.WriteJSON(map[string]any{"type": "register", "id": "r", "body": json.RawMessage(doc)}); err != nil {
		t.Fatal(err)
	}
	select {
	case frame, ok := <-results:
		if !ok {
			t.Fatal("connection closed before register_result")
		}
		out := map[string]any{}
		_ = json.Unmarshal(frame.Body, &out)
		return out
	case <-time.After(10 * time.Second):
		t.Fatal("no register_result")
		return nil
	}
}

// The full plan-0043 loop over one WebSocket: enroll → attach with a
// certification-gated offer → the broker runs the steps over the tunnel
// → pass freezes → /registry/offerings advertises → results on the
// admin API → operator re-run.
func TestCertificationGatesFreezeEndToEnd(t *testing.T) {
	t.Setenv("BROKER_ADMIN_TOKEN", "secret-token")
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "seal.key")
	if err := os.WriteFile(keyPath, []byte(strings.Repeat("ab", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	stateDir, _ := os.MkdirTemp("", "cert-e2e-*")
	t.Cleanup(func() {
		for i := 0; i < 50; i++ {
			if os.RemoveAll(stateDir) == nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
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
    certification:
      - { name: ready, type: readiness, config: { attempts: 2, interval_ms: 100 } }
      - name: smoke
        type: request
        config:
          transport: unary
          body: { model: "{{identity.openai.model}}", messages: [ { role: user, content: ping } ] }
          assert: [ "$.choices[0].message.content", { path: "$.usage.total_tokens", min: 1 } ]
      - { name: usage, type: usage, config: { min_units: 10 } }
`
	if err := os.WriteFile(configPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := mustServerFromPath(t, configPath)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	_, enr, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/enroll", `{"host_id":"h1"}`, nil)
	token := enr["credential"].(map[string]any)["token"].(string)

	c := dialAttach(t, ts)
	results := runnerSide(t, c, func(method, path string, headers map[string][]string, body []byte) (int, string, []byte) {
		if got := headers["Livepeer-Runner-Local-Id"]; len(got) == 0 || got[0] != "chat" {
			t.Errorf("missing local id header on %s", path)
		}
		u := path
		if i := strings.Index(u, "worker.local"); i >= 0 {
			u = u[i+len("worker.local"):]
		}
		switch {
		case strings.HasSuffix(u, "/ready"):
			return 200, "text/plain", []byte("ok")
		case strings.HasSuffix(u, "/v1/chat/completions"):
			var req map[string]any
			_ = json.Unmarshal(body, &req)
			if req["model"] != "llama" {
				return 400, "text/plain", []byte("wrong model")
			}
			return 200, "application/json",
				[]byte(`{"choices":[{"message":{"content":"pong"}}],"usage":{"total_tokens":17}}`)
		default:
			return 404, "text/plain", []byte("nope")
		}
	})
	res := registerVia(t, c, results, attachDoc(token, "h1", func(m map[string]any) {
		cap0 := m["capabilities"].([]any)[0].(map[string]any)
		cap0["identity"] = map[string]any{"openai.model": "llama"}
	}))
	if res["document"] != "accepted" {
		t.Fatalf("attach: %v", res)
	}

	// The run executes async over the tunnel; poll for the freeze.
	deadline := time.Now().Add(10 * time.Second)
	var ov map[string]any
	for time.Now().Before(deadline) {
		_, ov, _ = adminReq(t, srv, http.MethodGet, "/admin/v1/offers/llama-shared", "", nil)
		if ov["state"] == "frozen" {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if ov["state"] != "frozen" {
		_, certs, _ := adminReq(t, srv, http.MethodGet, "/admin/v1/certification", "", nil)
		t.Fatalf("offer never froze: %v\ncertification: %v", ov, certs)
	}
	frozenBy := ov["frozen"].(map[string]any)["frozen_by"].(map[string]any)
	if frozenBy["host_id"] != "h1" || frozenBy["run_id"] == "" {
		t.Fatalf("frozen_by: %v", frozenBy)
	}

	// Results on the admin API carry the steps with evidence.
	_, certs, _ := adminReq(t, srv, http.MethodGet, "/admin/v1/certification?latest=true", "", nil)
	runResults := certs["results"].([]any)
	if len(runResults) != 1 {
		t.Fatalf("results: %v", certs)
	}
	run := runResults[0].(map[string]any)
	if run["state"] != "passed" || run["trigger"] != "match" {
		t.Fatalf("run: %v", run)
	}
	steps := run["steps"].([]any)
	if len(steps) != 3 || steps[2].(map[string]any)["evidence"].(map[string]any)["units"] != float64(17) {
		t.Fatalf("steps: %v", steps)
	}

	// Advertised.
	if n := len(offeringsPayloadOf(t, srv)["capabilities"].([]any)); n != 1 {
		t.Fatalf("advertised tuples: %d", n)
	}

	// Operator re-run.
	code, rerun, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/certification/h1/llama-shared/run", `{}`, nil)
	if code != http.StatusAccepted || rerun["run_id"] == run["run_id"] {
		t.Fatalf("operator run: %d %v", code, rerun)
	}
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_, pair, _ := adminReq(t, srv, http.MethodGet, "/admin/v1/certification/h1/llama-shared", "", nil)
		rs := pair["results"].([]any)
		if len(rs) >= 2 && rs[0].(map[string]any)["state"] == "passed" && rs[0].(map[string]any)["trigger"] == "operator" {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("operator run never finished")
}

func TestCertificationRunNotMatched(t *testing.T) {
	srv := newOffersTestServer(t)
	code, res, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/certification/nope/llama-shared/run", `{}`, nil)
	if code != http.StatusNotFound {
		t.Fatalf("unknown host: %d %v", code, res)
	}
}
