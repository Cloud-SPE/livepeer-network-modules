package harness

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// The suite's own runner (protocols/runner-attach.md).
//
// A broker under test dispatches paid work down a runner's attach
// connection, so the suite has to be one: it attaches, declares what its
// fakes serve, and forwards what the broker sends to the right fake.
// This is the same shape a real agent has, written against the wire
// contract rather than importing one.

// RunnerSpec is one capability the suite's runner declares, plus where
// the agent side forwards work for it.
type RunnerSpec struct {
	LocalID           string
	CapabilityID      string
	Protocol          string
	Identity          map[string]string
	Transports        []string
	DescriptorSchemas []string
	Metering          string
	WorkUnitName      string
	Extractor         map[string]any
	Paths             map[string]string
	SchemaVersions    map[string]string
	// BaseURL is the fake this entry's work is forwarded to. Never sent
	// to the broker: the tunnel is the only way in.
	BaseURL string
}

// Runner is an attached conformance runner.
type Runner struct {
	conn      *websocket.Conn
	routes    map[string]string
	writeMu   sync.Mutex
	done      chan struct{}
	closeOnce sync.Once
}

// StartRunner attaches to the broker with the given credential and
// serves specs until Close. It returns once the broker has accepted the
// attach document, so a caller can then wait for offers to freeze.
func (c *Ctx) StartRunner(credential, hostID string, specs []RunnerSpec) (*Runner, error) {
	return StartRunner(c.BrokerURL, credential, hostID, specs)
}

// StartRunner is the same against a broker URL, for callers that attach
// before a Ctx exists — the suite's own runner has to be serving before
// the first scenario runs.
func StartRunner(brokerURL, credential, hostID string, specs []RunnerSpec) (*Runner, error) {
	if credential == "" {
		return nil, fmt.Errorf("no attach credential")
	}
	u := strings.Replace(strings.TrimRight(brokerURL, "/"), "http", "ws", 1) + AttachWSPath
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, resp, err := dialer.Dial(u, nil)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusNotFound {
			return nil, ErrAttachUnsupported
		}
		return nil, fmt.Errorf("dial attach: %w", err)
	}
	r := &Runner{conn: conn, routes: map[string]string{}, done: make(chan struct{})}
	caps := make([]any, 0, len(specs))
	for _, s := range specs {
		r.routes[s.LocalID] = strings.TrimRight(s.BaseURL, "/")
		entry := map[string]any{
			"capability_id":   s.CapabilityID,
			"protocol":        s.Protocol,
			"local_id":        s.LocalID,
			"work_unit":       workUnitOf(s),
			"paths":           s.Paths,
			"readiness":       map[string]any{"type": "http-status", "path": "/"},
			"identity":        s.Identity,
			"schema_versions": s.SchemaVersions,
		}
		if len(s.Transports) > 0 {
			entry["transports"] = s.Transports
		}
		if len(s.DescriptorSchemas) > 0 {
			entry["descriptor_schemas"] = s.DescriptorSchemas
		}
		if s.Metering != "" {
			entry["metering"] = s.Metering
		}
		caps = append(caps, entry)
	}
	doc := map[string]any{
		"contract_version": "1.0",
		"credential":       map[string]any{"kind": "bearer", "token": credential},
		"host_id":          hostID,
		"agent_version":    "livepeer-conformance/runner",
		"hardware":         []any{},
		"capabilities":     caps,
	}
	body, err := json.Marshal(doc)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := r.send(TunnelFrame{Type: "register", ID: "register", Body: body}); err != nil {
		_ = conn.Close()
		return nil, err
	}
	res, err := r.readRegisterResult(20 * time.Second)
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	if res.Document != "accepted" {
		_ = conn.Close()
		return nil, fmt.Errorf("attach rejected: %+v", res.Reasons)
	}
	for _, cap := range res.Capabilities {
		if cap.Status != "accepted" {
			_ = conn.Close()
			return nil, fmt.Errorf("capability %s rejected: %+v", cap.LocalID, cap.Reasons)
		}
	}
	go r.serve()
	return r, nil
}

// TunnelFrame is the tunnel's message envelope.
type TunnelFrame struct {
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

func workUnitOf(s RunnerSpec) map[string]any {
	wu := map[string]any{"name": s.WorkUnitName}
	if s.Extractor != nil {
		wu["extractor"] = s.Extractor
	}
	return wu
}

func (r *Runner) send(f TunnelFrame) error {
	r.writeMu.Lock()
	defer r.writeMu.Unlock()
	_ = r.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
	return r.conn.WriteJSON(f)
}

func (r *Runner) readRegisterResult(timeout time.Duration) (*AttachResult, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_ = r.conn.SetReadDeadline(deadline)
		var f TunnelFrame
		if err := r.conn.ReadJSON(&f); err != nil {
			return nil, fmt.Errorf("read register_result: %w", err)
		}
		if f.Type != "register_result" {
			continue
		}
		var res AttachResult
		if err := json.Unmarshal(f.Body, &res); err != nil {
			return nil, err
		}
		return &res, nil
	}
	return nil, fmt.Errorf("no register_result within %s", timeout)
}

// serve forwards dispatched requests to the fake each local_id names.
func (r *Runner) serve() {
	defer r.Close()
	for {
		_ = r.conn.SetReadDeadline(time.Time{})
		var msg TunnelFrame
		if err := r.conn.ReadJSON(&msg); err != nil {
			return
		}
		if msg.Type != "request" {
			continue
		}
		go func(m TunnelFrame) {
			if err := r.send(r.forward(m)); err != nil {
				return
			}
		}(msg)
	}
}

func (r *Runner) forward(msg TunnelFrame) TunnelFrame {
	out := TunnelFrame{Type: "response", ID: msg.ID}
	localID := headerOf(msg.Headers, "Livepeer-Runner-Local-Id")
	base := r.routes[localID]
	if base == "" {
		out.Error = "unknown local_id " + localID
		return out
	}
	target, err := joinURL(base, msg.URL)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	body, err := base64.StdEncoding.DecodeString(msg.BodyBase64)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	method := msg.Method
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequest(method, target, strings.NewReader(string(body)))
	if err != nil {
		out.Error = err.Error()
		return out
	}
	req.Header = http.Header(msg.Headers).Clone()
	req.Header.Del("Livepeer-Runner-Local-Id")
	resp, err := (&http.Client{Timeout: 90 * time.Second}).Do(req)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.StatusCode = resp.StatusCode
	out.Headers = map[string][]string(resp.Header)
	out.BodyBase64 = base64.StdEncoding.EncodeToString(respBody)
	return out
}

// Close ends the attach session.
func (r *Runner) Close() {
	r.closeOnce.Do(func() {
		close(r.done)
		_ = r.conn.Close()
	})
}

func headerOf(h map[string][]string, key string) string {
	for k, v := range h {
		if strings.EqualFold(k, key) && len(v) > 0 {
			return strings.TrimSpace(v[0])
		}
	}
	return ""
}

func joinURL(base, tunnelURL string) (string, error) {
	b, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil {
		return "", err
	}
	t, err := url.Parse(tunnelURL)
	if err != nil {
		return "", err
	}
	b.Path = strings.TrimRight(b.Path, "/") + "/" + strings.TrimLeft(t.Path, "/")
	b.RawQuery = t.RawQuery
	return b.String(), nil
}
