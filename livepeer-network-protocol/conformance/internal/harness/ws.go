package harness

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Control-WS client for the optional paid-session §8 binding. The
// binding is spec-OPTIONAL, so scenarios using this must skip when an
// implementation advertises no control.events_ws.

// WSFrame is the binding's wire shape in both directions.
type WSFrame struct {
	Type string         `json:"type"`
	Body map[string]any `json:"body,omitempty"`
}

// WSConn is an attached control-WS connection.
type WSConn struct{ c *websocket.Conn }

// DialControlWS attaches to a session's control-WS with the session
// credential. The second return is the HTTP response, so a scenario can
// assert the uniform-401 discipline on a rejected upgrade.
func (c *Ctx) DialControlWS(eventsWS, credential string) (*WSConn, *http.Response, error) {
	if eventsWS == "" {
		return nil, nil, fmt.Errorf("no events_ws advertised")
	}
	hdr := http.Header{}
	if credential != "" {
		hdr.Set("Authorization", "Bearer "+credential)
	}
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, resp, err := d.Dial(eventsWS, hdr)
	if err != nil {
		return nil, resp, err
	}
	return &WSConn{c: conn}, resp, nil
}

// Read returns the next frame, or an error on timeout.
func (w *WSConn) Read(timeout time.Duration) (WSFrame, error) {
	_ = w.c.SetReadDeadline(time.Now().Add(timeout))
	var f WSFrame
	err := w.c.ReadJSON(&f)
	return f, err
}

// ReadUntil reads frames until one matches type, or the budget expires.
// Push frames may interleave, so scenarios must not assume ordering.
func (w *WSConn) ReadUntil(frameType string, budget time.Duration) (WSFrame, error) {
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		f, err := w.Read(time.Until(deadline))
		if err != nil {
			return WSFrame{}, err
		}
		if f.Type == frameType {
			return f, nil
		}
	}
	return WSFrame{}, fmt.Errorf("no %s frame within %s", frameType, budget)
}

// Send writes a gateway→broker frame.
func (w *WSConn) Send(f WSFrame) error {
	_ = w.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return w.c.WriteJSON(f)
}

// SendTopUp sends a session.topup frame carrying a payment envelope.
func (w *WSConn) SendTopUp(payment string) error {
	return w.Send(WSFrame{Type: "session.topup", Body: map[string]any{"payment_header": payment}})
}

// Close closes the connection.
func (w *WSConn) Close() { _ = w.c.Close() }

// WSURLFromControl extracts control.events_ws from an open response.
func WSURLFromControl(m map[string]any) string {
	u := FieldString(m, "control.events_ws")
	if u == "" {
		return ""
	}
	// Tolerate an http(s) URL where an implementation forgot the scheme swap.
	if strings.HasPrefix(u, "http://") {
		return "ws://" + strings.TrimPrefix(u, "http://")
	}
	if strings.HasPrefix(u, "https://") {
		return "wss://" + strings.TrimPrefix(u, "https://")
	}
	return u
}

// B64 is a convenience for building payment headers in WS frames.
func B64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
