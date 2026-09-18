package harness

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Runner-attach client (protocols/runner-attach.md). The suite plays the
// runner: it opens the broker's WebSocket attach fallback, sends one
// `register` frame carrying an attach document, and reads the
// `register_result`. QUIC is the preferred transport in production;
// the contract is identical on both and the WS path is the one a
// black-box suite can drive without a QUIC stack.

// AttachWSPath is the broker's WebSocket attach endpoint (plan 0040
// §6.1 fallback; runner-attach §2).
const AttachWSPath = "/internal/v1/worker/session"

// AttachDoc is a runner attach document. It is a plain map so scenarios
// can add unknown fields on purpose — the contract's rejection rules are
// half the point.
type AttachDoc map[string]any

// AttachResult is the broker's register_result (runner-attach §6).
type AttachResult struct {
	ContractVersion string             `json:"contract_version"`
	Document        string             `json:"document"`
	HostID          string             `json:"host_id"`
	Reasons         []AttachReason     `json:"reasons"`
	Capabilities    []AttachCapability `json:"capabilities"`
}

type AttachReason struct {
	Code     string `json:"code"`
	Field    string `json:"field,omitempty"`
	Declared string `json:"declared,omitempty"`
	Expected string `json:"expected,omitempty"`
	Message  string `json:"message,omitempty"`
}

type AttachCapability struct {
	Index        int            `json:"index"`
	LocalID      string         `json:"local_id"`
	CapabilityID string         `json:"capability_id"`
	Status       string         `json:"status"`
	Reasons      []AttachReason `json:"reasons,omitempty"`
	Warnings     []AttachReason `json:"warnings,omitempty"`
}

// AttachConn is an open attach connection.
type AttachConn struct{ c *websocket.Conn }

// ErrAttachUnsupported means the broker-under-test does not serve the
// attach endpoint at all. Scenarios wrap it in ErrSkip: a broker that
// predates plan 0043 item 7 is not in violation, it is out of scope.
var ErrAttachUnsupported = fmt.Errorf("broker does not serve %s", AttachWSPath)

// DialAttach opens the attach WebSocket. A 404 (or any non-upgrade)
// is reported as ErrAttachUnsupported.
func (c *Ctx) DialAttach() (*AttachConn, error) {
	u := strings.Replace(strings.TrimRight(c.BrokerURL, "/"), "http", "ws", 1) + AttachWSPath
	d := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, resp, err := d.Dial(u, nil)
	if err != nil {
		if resp != nil && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed) {
			return nil, ErrAttachUnsupported
		}
		return nil, fmt.Errorf("dial attach: %w", err)
	}
	return &AttachConn{c: conn}, nil
}

// Register sends the document as a `register` frame and reads the
// `register_result`. The frame envelope is {type, id, body}: the
// tunnel's own message shape, with the attach document as body.
func (a *AttachConn) Register(doc AttachDoc) (*AttachResult, error) {
	_ = a.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := a.c.WriteJSON(map[string]any{"type": "register", "id": "reg-1", "body": doc}); err != nil {
		return nil, fmt.Errorf("send register: %w", err)
	}
	return a.ReadResult(15 * time.Second)
}

// ReadResult waits for a register_result frame.
func (a *AttachConn) ReadResult(timeout time.Duration) (*AttachResult, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_ = a.c.SetReadDeadline(deadline)
		var frame struct {
			Type string          `json:"type"`
			Body json.RawMessage `json:"body"`
		}
		if err := a.c.ReadJSON(&frame); err != nil {
			return nil, fmt.Errorf("read register_result: %w", err)
		}
		if frame.Type != "register_result" {
			continue
		}
		var res AttachResult
		if err := json.Unmarshal(frame.Body, &res); err != nil {
			return nil, fmt.Errorf("decode register_result: %w", err)
		}
		return &res, nil
	}
	return nil, fmt.Errorf("no register_result within %s", timeout)
}

// SendRaw writes an arbitrary frame (for the "anything before register
// closes the connection" rule).
func (a *AttachConn) SendRaw(frame map[string]any) error {
	_ = a.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return a.c.WriteJSON(frame)
}

// ExpectClosed reports nil if the broker closes the connection within
// the timeout.
func (a *AttachConn) ExpectClosed(timeout time.Duration) error {
	_ = a.c.SetReadDeadline(time.Now().Add(timeout))
	for {
		if _, _, err := a.c.ReadMessage(); err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.ClosePolicyViolation,
				websocket.CloseProtocolError, websocket.CloseAbnormalClosure) || strings.Contains(err.Error(), "close") ||
				strings.Contains(err.Error(), "EOF") {
				return nil
			}
			if strings.Contains(err.Error(), "timeout") {
				return fmt.Errorf("connection still open after %s", timeout)
			}
			return nil
		}
	}
}

func (a *AttachConn) Close() { _ = a.c.Close() }

// Reason finds a reason by code in a list.
func Reason(rs []AttachReason, code string) *AttachReason {
	for i := range rs {
		if rs[i].Code == code {
			return &rs[i]
		}
	}
	return nil
}
