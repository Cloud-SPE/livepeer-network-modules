package server

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// End-to-end control-WS test: attach with the credential, receive
// usage.tick + balance pushes, drive topup and end frames with acks,
// and observe the terminal session.ended push.

func wsDial(t *testing.T, srvURL, sessionID, credential string) (*websocket.Conn, *http.Response) {
	t.Helper()
	u := "ws" + strings.TrimPrefix(srvURL, "http") + "/v1/session/" + sessionID + "/ws"
	hdr := http.Header{}
	if credential != "" {
		hdr.Set("Authorization", "Bearer "+credential)
	}
	conn, resp, err := websocket.DefaultDialer.Dial(u, hdr)
	if err != nil && resp == nil {
		t.Fatalf("dial: %v", err)
	}
	return conn, resp
}

func wsRead(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var f map[string]any
	if err := conn.ReadJSON(&f); err != nil {
		t.Fatalf("read frame: %v", err)
	}
	return f
}

func TestSessionControlWS(t *testing.T) {
	srv, runner := newSessionTestServer(t)

	open := decode(t, sessionOpenReq(t, srv, "req-ws-1"))
	sessionID := open["session_id"].(string)
	credential := open["credential"].(string)

	// events_ws advertised in control URLs
	control := open["control"].(map[string]any)
	if ws, _ := control["events_ws"].(string); !strings.Contains(ws, "/v1/session/"+sessionID+"/ws") {
		t.Fatalf("events_ws not advertised: %v", control)
	}

	// Bad credential: rejected before upgrade with the uniform 401.
	if conn, resp := wsDial(t, srv.URL, sessionID, "sc_wrong"); conn != nil {
		t.Fatal("upgrade succeeded with bad credential")
	} else if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad-credential upgrade status %d", resp.StatusCode)
	}

	conn, _ := wsDial(t, srv.URL, sessionID, credential)
	if conn == nil {
		t.Fatal("upgrade failed with valid credential")
	}
	defer conn.Close()

	// A runner usage event pushes usage.tick then session.balance.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/session/"+sessionID+"/events",
		strings.NewReader(`{"event_id":"evt_ws1","sequence":1,"event_type":"session.usage.tick","usage":{"unit":"participant_minutes","total":6}}`))
	req.Header.Set("Authorization", "Bearer "+runner.callbackToken)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("event post: %v %d", err, resp.StatusCode)
	}
	tick := wsRead(t, conn)
	if tick["type"] != "session.usage.tick" {
		t.Fatalf("first frame %v, want session.usage.tick", tick["type"])
	}
	if got := tick["body"].(map[string]any)["claimed_total"].(float64); got != 6 {
		t.Fatalf("tick claimed_total %v, want 6", got)
	}
	bal := wsRead(t, conn)
	if bal["type"] != "session.balance" {
		t.Fatalf("second frame %v, want session.balance", bal["type"])
	}
	if _, ok := bal["body"].(map[string]any)["will_refuse_next_refill"]; !ok {
		t.Fatalf("balance frame missing will_refuse_next_refill: %v", bal["body"])
	}

	// Gateway-initiated topup frame is acknowledged.
	topup := map[string]any{"type": "session.topup", "body": map[string]any{
		"payment_header": base64.StdEncoding.EncodeToString([]byte("ws-topup")),
	}}
	if err := conn.WriteJSON(topup); err != nil {
		t.Fatal(err)
	}
	ack := wsRead(t, conn)
	if ack["type"] != "ack" || ack["body"].(map[string]any)["op"] != "session.topup" {
		t.Fatalf("topup ack: %v", ack)
	}

	// Unknown frame type gets an error frame, not a dropped message.
	_ = conn.WriteJSON(map[string]any{"type": "session.dance"})
	errf := wsRead(t, conn)
	if errf["type"] != "error" || errf["body"].(map[string]any)["code"] != "unknown_frame" {
		t.Fatalf("unknown frame handling: %v", errf)
	}

	// Gateway-initiated end: ack plus the terminal session.ended push.
	_ = conn.WriteJSON(map[string]any{"type": "session.end", "body": map[string]any{"reason": "gateway_close"}})
	sawAck, sawEnded := false, false
	for i := 0; i < 2; i++ {
		f := wsRead(t, conn)
		switch f["type"] {
		case "ack":
			if f["body"].(map[string]any)["op"] == "session.end" {
				sawAck = true
			}
		case "session.ended":
			if f["body"].(map[string]any)["close_reason"] == "gateway_close" {
				sawEnded = true
			}
		}
	}
	if !sawAck || !sawEnded {
		t.Fatalf("end flow incomplete: ack=%v ended=%v", sawAck, sawEnded)
	}

	// Post-terminal topup over WS: refused with the stable code.
	_ = conn.WriteJSON(topup)
	refused := wsRead(t, conn)
	if refused["type"] != "error" || refused["body"].(map[string]any)["code"] != "refill_refused" {
		t.Fatalf("post-terminal topup: %v", refused)
	}
}
