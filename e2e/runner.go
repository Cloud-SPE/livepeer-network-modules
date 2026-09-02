package e2e

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeRunner is a member's host as the broker sees one: it attaches
// outbound, declares what it serves, and answers dispatched work.
//
// It speaks the wire rather than importing the agent, so a change that
// the agent and the broker agree on but the CONTRACT does not still
// fails here.
type fakeRunner struct {
	t        *testing.T
	conn     *websocket.Conn
	writeMu  sync.Mutex
	mu       sync.Mutex
	result   map[string]any
	requests []string
	closed   bool

	// Session state: what the broker handed this runner at create, and
	// how the broker answered its usage report.
	mode           runnerMode
	results        chan map[string]any
	callbackURL    string
	callbackToken  string
	callbackStatus int
}

// tunnelFrame is the attach tunnel's message shape.
type tunnelFrame struct {
	Type       string              `json:"type"`
	ID         string              `json:"id"`
	Body       json.RawMessage     `json:"body,omitempty"`
	Method     string              `json:"method,omitempty"`
	URL        string              `json:"url,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	BodyBase64 string              `json:"body_base64,omitempty"`
	StatusCode int                 `json:"status_code,omitempty"`
	Error      string              `json:"error,omitempty"`
}

// runnerMode is what this runner serves.
type runnerMode int

const (
	// modeJob answers dispatched job requests with a usage-bearing body.
	modeJob runnerMode = iota
	// modeSession answers the paid-session lifecycle and reports usage.
	modeSession
	// modeSilentSession opens and terminates sessions correctly and
	// never reports usage — a runner that works but cannot be billed.
	modeSilentSession
)

// attach dials the broker and registers the given document.
//
// The read loop starts BEFORE the register frame goes out, and the
// register result arrives on a channel like any other frame. That is
// not incidental: the broker registers a runner before acknowledging
// it, so work can be dispatched while the acknowledgement is still in
// flight. A helper that read frames itself while waiting for the result
// would swallow that first request and the broker would time out
// waiting for a reply it was never going to get.
func attach(t *testing.T, brokerURL string, document map[string]any, mode runnerMode) *fakeRunner {
	t.Helper()
	url := strings.Replace(strings.TrimRight(brokerURL, "/"), "http", "ws", 1) + "/internal/v1/worker/session"
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, resp, err := dialer.Dial(url, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial attach (%d): %v", status, err)
	}
	r := &fakeRunner{t: t, conn: conn, mode: mode, results: make(chan map[string]any, 4)}
	t.Cleanup(r.close)
	go r.serve()
	r.register(document)
	return r
}

// register sends a document and waits for the broker's verdict.
//
// The verdict is the assertion that matters most in this suite: it is
// where a field the controller sends and the broker does not admit
// shows up, and it is the failure that takes every other runner on the
// host down with it.
func (r *fakeRunner) register(document map[string]any) {
	r.t.Helper()
	body, err := json.Marshal(document)
	if err != nil {
		r.t.Fatalf("marshal document: %v", err)
	}
	r.send(tunnelFrame{Type: "register", ID: "register", Body: body})
	select {
	case result := <-r.results:
		r.mu.Lock()
		r.result = result
		r.mu.Unlock()
	case <-time.After(20 * time.Second):
		r.t.Fatal("the broker never answered the register frame")
	}
}

// requireAccepted fails with the broker's own reasons, which name the
// offending field.
func (r *fakeRunner) requireAccepted() {
	r.t.Helper()
	r.mu.Lock()
	result := r.result
	r.mu.Unlock()
	if got, _ := result["document"].(string); got != "accepted" {
		raw, _ := json.MarshalIndent(result, "", "  ")
		r.t.Fatalf("the broker refused this host's attach document — every runner on the host is down:\n%s", raw)
	}
}

// acceptedLocalIDs are the capabilities the broker took.
func (r *fakeRunner) acceptedLocalIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	caps, _ := r.result["capabilities"].([]any)
	for _, entry := range caps {
		item, _ := entry.(map[string]any)
		if item == nil {
			continue
		}
		if status, _ := item["status"].(string); status != "accepted" {
			continue
		}
		if id, _ := item["local_id"].(string); id != "" {
			out = append(out, id)
		}
	}
	return out
}

// serve is the runner's single reader: it hands register results to
// whoever is waiting and answers dispatched work.
func (r *fakeRunner) serve() {
	for {
		var frame tunnelFrame
		if err := r.conn.ReadJSON(&frame); err != nil {
			return
		}
		switch frame.Type {
		case "register_result":
			var result map[string]any
			if err := json.Unmarshal(frame.Body, &result); err != nil {
				r.t.Logf("decode register_result: %v", err)
				continue
			}
			select {
			case r.results <- result:
			default:
			}
			continue
		case "request":
		default:
			continue
		}

		local := ""
		if v := frame.Headers["Livepeer-Runner-Local-Id"]; len(v) > 0 {
			local = v[0]
		}
		r.mu.Lock()
		r.requests = append(r.requests, local+" "+frame.URL)
		mode := r.mode
		r.mu.Unlock()

		if mode != modeJob {
			if status, out, ok := r.handleSession(frame); ok {
				r.send(tunnelFrame{
					Type: "response", ID: frame.ID, StatusCode: status,
					Headers:    map[string][]string{"Content-Type": {"application/json"}},
					BodyBase64: base64.StdEncoding.EncodeToString([]byte(out)),
				})
				continue
			}
		}

		body := `{"choices":[{"message":{"content":"ok"}}],"usage":{"total_tokens":7}}`
		r.send(tunnelFrame{
			Type: "response", ID: frame.ID, StatusCode: http.StatusOK,
			Headers:    map[string][]string{"Content-Type": {"application/json"}},
			BodyBase64: base64.StdEncoding.EncodeToString([]byte(body)),
		})
	}
}

func (r *fakeRunner) served() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.requests...)
}

// send serialises writes: gorilla allows one writer, and the serve loop
// and a re-register can both reach for the socket.
func (r *fakeRunner) send(frame tunnelFrame) {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if r.closed {
		return
	}
	if err := r.conn.WriteJSON(frame); err != nil {
		r.t.Logf("tunnel write: %v", err)
	}
}

func (r *fakeRunner) close() {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	if r.closed {
		return
	}
	r.closed = true
	_ = r.conn.Close()
}

// hostDocument is the attach document a pool-managed host sends.
//
// Built here from the same facts the controller puts in its desired
// state, so if the controller stops supplying one of them this suite
// notices rather than the member's host.
func hostDocument(hostID, credential, localID, capability, model, gpuUUID string) map[string]any {
	return map[string]any{
		"contract_version": "1.1",
		"credential":       map[string]any{"kind": "bearer", "token": credential},
		"host_id":          hostID,
		"agent_version":    "e2e/1",
		"hardware": []any{map[string]any{
			"gpu_uuid": gpuUUID, "gpu_model": "NVIDIA GeForce RTX 4090",
			"vram_bytes": 25769803776,
		}},
		"capabilities": []any{map[string]any{
			"capability_id": capability,
			"protocol":      "paid-job/v1",
			"local_id":      localID,
			"transports":    []any{"unary"},
			"work_unit": map[string]any{
				"name":      "tokens",
				"extractor": map[string]any{"type": "openai-usage", "field": "total_tokens"},
			},
			"paths":           map[string]any{"invoke": "/v1/chat/completions"},
			"readiness":       map[string]any{"type": "http-status", "path": "/healthz"},
			"identity":        map[string]any{"openai.model": model},
			"schema_versions": map[string]any{"paid-job/v1": "1.0.0"},
			"devices":         []any{gpuUUID},
		}},
	}
}

func mustField(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, _ := m[key].(string)
	if v == "" {
		raw, _ := json.MarshalIndent(m, "", "  ")
		t.Fatalf("expected %q in:\n%s", key, raw)
	}
	return v
}

// sessionDocument is the attach document a paid-session host sends.
func sessionDocument(hostID, credential, localID, gpuUUID string) map[string]any {
	return map[string]any{
		"contract_version": "1.1",
		"credential":       map[string]any{"kind": "bearer", "token": credential},
		"host_id":          hostID,
		"agent_version":    "e2e/1",
		"hardware": []any{map[string]any{
			"gpu_uuid": gpuUUID, "gpu_model": "NVIDIA GeForce RTX 4090",
			"vram_bytes": 25769803776,
		}},
		"capabilities": []any{map[string]any{
			"capability_id": "meet:sfu-room",
			"protocol":      "paid-session/v1",
			"local_id":      localID,
			// No transports: a paid-session capability declares
			// descriptor schemas instead, and the broker rejects the
			// whole host document if it carries both.
			"descriptor_schemas": []any{"sfu-room/v1"},
			"metering":           "runner-reported",
			"work_unit":          map[string]any{"name": "seconds"},
			"paths": map[string]any{
				"create": "/sessions", "status": "/sessions/{id}", "terminate": "/sessions/{id}",
			},
			"readiness": map[string]any{"type": "http-status", "path": "/healthz"},
			"identity":  map[string]any{"provider": "e2e"},
			// One entry for the protocol and one for every descriptor
			// schema; a missing entry rejects the capability.
			"schema_versions": map[string]any{"paid-session/v1": "1.0.0", "sfu-room/v1": "1.0.0"},
			"devices":         []any{gpuUUID},
		}},
	}
}

// handleSession answers the paid-session lifecycle and, unless the
// runner is deliberately silent, reports usage to whatever callback the
// broker handed it.
//
// It reports on a real outbound HTTP request, to the URL in the create
// body, exactly as it would against a paid session's callback. That is
// the point: if the broker's external_base_url is wrong, or the route
// is not registered, or the token does not verify, this is where it
// shows — and nowhere in the unit tests, where the callback is a
// function call.
// handleSession answers one session-shaped request. Returns false when
// the request is not a session call.
func (r *fakeRunner) handleSession(frame tunnelFrame) (int, string, bool) {
	path := frame.URL
	if idx := strings.Index(path, "://"); idx >= 0 {
		if slash := strings.Index(path[idx+3:], "/"); slash >= 0 {
			path = path[idx+3+slash:]
		}
	}
	switch {
	case frame.Method == http.MethodPost && path == "/sessions":
		var body struct {
			CallbackURL   string `json:"callback_url"`
			CallbackToken string `json:"callback_token"`
		}
		if raw, err := base64.StdEncoding.DecodeString(frame.BodyBase64); err == nil {
			_ = json.Unmarshal(raw, &body)
		}
		r.mu.Lock()
		r.callbackURL, r.callbackToken = body.CallbackURL, body.CallbackToken
		reports := r.mode == modeSession
		r.mu.Unlock()
		if reports && body.CallbackURL != "" {
			go r.reportUsage(body.CallbackURL, body.CallbackToken, "seconds", 5)
		}
		return http.StatusOK,
			`{"runner_session_id":"rs-1","runtime":{"schema":"sfu-room/v1","public":{"join_url":"wss://x/join"}}}`,
			true
	case frame.Method == http.MethodDelete && strings.HasPrefix(path, "/sessions/"):
		return http.StatusNoContent, "", true
	}
	return 0, "", false
}

// reportUsage posts a cumulative usage claim, the envelope
// paid-session/v1 §7.2 describes.
func (r *fakeRunner) reportUsage(url, token, unit string, total uint64) {
	body, _ := json.Marshal(map[string]any{
		"event_id": "ev-1", "sequence": 1, "event_type": "usage",
		"usage": map[string]any{"unit": unit, "total": total},
	})
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Logf("usage callback to %s failed: %v", url, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	r.mu.Lock()
	r.callbackStatus = resp.StatusCode
	r.mu.Unlock()
}

// callback reports what the broker handed the runner and how the report
// was received.
func (r *fakeRunner) callback() (url, token string, status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.callbackURL, r.callbackToken, r.callbackStatus
}
