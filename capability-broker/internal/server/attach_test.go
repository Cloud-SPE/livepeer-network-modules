package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/workerconn"
	"github.com/gorilla/websocket"
)

func attachDoc(token, hostID string, mut func(map[string]any)) []byte {
	m := map[string]any{
		"contract_version": "1.0",
		"credential":       map[string]any{"kind": "bearer", "token": token},
		"host_id":          hostID,
		"agent_version":    "test/1",
		"hardware":         []any{map[string]any{"gpu_uuid": "GPU-1", "gpu_model": "Test GPU", "vram_bytes": 8 << 30}},
		"capabilities": []any{map[string]any{
			"capability_id": "openai:chat-completions", "protocol": "paid-job/v1", "local_id": "chat",
			"transports":      []any{"unary"},
			"work_unit":       map[string]any{"name": "tokens", "extractor": map[string]any{"type": "openai-usage"}},
			"paths":           map[string]any{"invoke": "/v1/chat/completions"},
			"readiness":       map[string]any{"type": "http-status", "path": "/ready"},
			"identity":        map[string]any{"openai.model": "m"},
			"schema_versions": map[string]any{"paid-job/v1": "1.0.15"},
			"x-quant":         "fp8",
		}},
	}
	if mut != nil {
		mut(m)
	}
	b, _ := json.Marshal(m)
	return b
}

func dialAttach(t *testing.T, ts *httptest.Server) *websocket.Conn {
	t.Helper()
	u := "ws" + strings.TrimPrefix(ts.URL, "http") + "/internal/v1/worker/session"
	c, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func register(t *testing.T, c *websocket.Conn, doc []byte) map[string]any {
	t.Helper()
	if err := c.WriteJSON(map[string]any{"type": "register", "id": "r", "body": json.RawMessage(doc)}); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	var frame workerconn.TunnelMessage
	if err := c.ReadJSON(&frame); err != nil {
		t.Fatalf("read result: %v", err)
	}
	if frame.Type != workerconn.MessageTypeRegisterResult {
		t.Fatalf("frame type %q", frame.Type)
	}
	out := map[string]any{}
	_ = json.Unmarshal(frame.Body, &out)
	return out
}

func TestAttachOverWebSocket(t *testing.T) {
	srv := newCredentialTestServer(t, true)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	_, enr, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/enroll", `{"host_id":"host-1","label":"rig"}`, nil)
	token := enr["credential"].(map[string]any)["token"].(string)

	// Bad credential: rejected, indistinguishable reason, nothing registered.
	c := dialAttach(t, ts)
	res := register(t, c, attachDoc("lpc_nope", "host-1", nil))
	if res["document"] != "rejected" || res["reasons"].([]any)[0].(map[string]any)["code"] != "credential_rejected" {
		t.Fatalf("bad credential: %v", res)
	}
	_ = c.Close()

	// Unknown field: document rejected naming the pointer.
	c = dialAttach(t, ts)
	res = register(t, c, attachDoc(token, "host-1", func(m map[string]any) { m["price"] = 1 }))
	r0 := res["reasons"].([]any)[0].(map[string]any)
	if res["document"] != "rejected" || r0["code"] != "unknown_field" || r0["field"] != "/price" {
		t.Fatalf("unknown field: %v", res)
	}
	_ = c.Close()

	// Accepted: visible on the admin API with declared facts and extensions.
	c = dialAttach(t, ts)
	res = register(t, c, attachDoc(token, "host-1", nil))
	if res["document"] != "accepted" || res["host_id"] != "host-1" {
		t.Fatalf("accept: %v", res)
	}
	caps := res["capabilities"].([]any)
	if len(caps) != 1 || caps[0].(map[string]any)["status"] != "accepted" || caps[0].(map[string]any)["local_id"] != "chat" {
		t.Fatalf("capabilities: %v", caps)
	}
	code, list, _ := adminReq(t, srv, http.MethodGet, "/admin/v1/runners", "", nil)
	runners := list["runners"].([]any)
	if code != http.StatusOK || len(runners) != 1 {
		t.Fatalf("runners: %d %v", code, list)
	}
	rv := runners[0].(map[string]any)
	if rv["host_id"] != "host-1" || rv["state"] != "connected" || rv["enrollment"].(map[string]any)["label"] != "rig" {
		t.Fatalf("runner view: %v", rv)
	}
	cv := rv["capabilities"].([]any)[0].(map[string]any)
	declared := cv["declared"].(map[string]any)
	if declared["identity"].(map[string]any)["openai.model"] != "m" || cv["extensions"].(map[string]any)["x-quant"] != "fp8" {
		t.Fatalf("declared/extensions: %v", cv)
	}
	if _, leaked := declared["paths"]; leaked {
		t.Fatal("paths shown without include=paths")
	}
	if len(rv["hardware"].([]any)) != 1 {
		t.Fatalf("hardware: %v", rv["hardware"])
	}

	// Re-send with a rejected second entry: document accepted, entry
	// rejected with both sides named, first entry still served.
	res = register(t, c, attachDoc(token, "host-1", func(m map[string]any) {
		bad := map[string]any{
			"capability_id": "openai:audio-transcriptions", "protocol": "paid-job/v1", "local_id": "whisper",
			"transports":      []any{"multipart"},
			"work_unit":       map[string]any{"name": "seconds", "extractor": map[string]any{"type": "whisper-seconds"}},
			"paths":           map[string]any{"invoke": "/v1/audio"},
			"readiness":       map[string]any{"type": "http-status"},
			"identity":        map[string]any{},
			"schema_versions": map[string]any{"paid-job/v1": "1.0.15"},
		}
		m["capabilities"] = append(m["capabilities"].([]any), bad)
	}))
	caps = res["capabilities"].([]any)
	if res["document"] != "accepted" || len(caps) != 2 || caps[1].(map[string]any)["status"] != "rejected" {
		t.Fatalf("resend: %v", res)
	}
	reason := caps[1].(map[string]any)["reasons"].([]any)[0].(map[string]any)
	if reason["code"] != "extractor_unknown" || reason["declared"] != "whisper-seconds" || reason["expected"] == "" {
		t.Fatalf("reason: %v", reason)
	}
	// The registry reflects the replacement (2 entries now, 1 rejected).
	_, one, _ := adminReq(t, srv, http.MethodGet, "/admin/v1/runners/host-1", "", nil)
	if n := len(one["capabilities"].([]any)); n != 2 {
		t.Fatalf("after resend: %d capabilities", n)
	}

	// Revoke kills the attach connection; the host shows disconnected.
	credID := enr["credential_id"].(string)
	code, rev, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/credentials/"+credID+"/revoke", `{"reason":"test"}`, nil)
	if code != http.StatusOK || rev["connections_closed"] != float64(1) {
		t.Fatalf("revoke: %d %v", code, rev)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("connection still open after revoke")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, one, _ = adminReq(t, srv, http.MethodGet, "/admin/v1/runners/host-1", "", nil)
		if one["state"] == "disconnected" || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if one["state"] != "disconnected" {
		t.Fatalf("state after revoke: %v", one["state"])
	}
}

func TestAttachRequiresRegisterFirst(t *testing.T) {
	srv := newCredentialTestServer(t, true)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()
	c := dialAttach(t, ts)
	if err := c.WriteJSON(map[string]any{"type": "request", "id": "early", "method": "GET", "url": "/"}); err != nil {
		t.Fatal(err)
	}
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("broker did not close a connection that spoke before register")
	}
	if _, list, _ := adminReq(t, srv, http.MethodGet, "/admin/v1/runners", "", nil); len(list["runners"].([]any)) != 0 {
		t.Fatal("something registered")
	}
}
