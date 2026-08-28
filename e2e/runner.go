package e2e

import (
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

// attach dials the broker and registers the given document.
func attach(t *testing.T, brokerURL string, document map[string]any) *fakeRunner {
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
	r := &fakeRunner{t: t, conn: conn}
	t.Cleanup(r.close)
	r.register(document)
	go r.serve()
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
	_ = r.conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	for {
		var frame tunnelFrame
		if err := r.conn.ReadJSON(&frame); err != nil {
			r.t.Fatalf("read register_result: %v", err)
		}
		if frame.Type != "register_result" {
			continue
		}
		var result map[string]any
		if err := json.Unmarshal(frame.Body, &result); err != nil {
			r.t.Fatalf("decode register_result: %v", err)
		}
		_ = r.conn.SetReadDeadline(time.Time{})
		r.mu.Lock()
		r.result = result
		r.mu.Unlock()
		return
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

// serve answers dispatched work the way a real runner would.
func (r *fakeRunner) serve() {
	for {
		var frame tunnelFrame
		if err := r.conn.ReadJSON(&frame); err != nil {
			return
		}
		if frame.Type != "request" {
			continue
		}
		local := ""
		if v := frame.Headers["Livepeer-Runner-Local-Id"]; len(v) > 0 {
			local = v[0]
		}
		r.mu.Lock()
		r.requests = append(r.requests, local+" "+frame.URL)
		r.mu.Unlock()

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
