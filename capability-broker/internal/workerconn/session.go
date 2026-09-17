package workerconn

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/gorilla/websocket"
)

const (
	MessageTypeRequest  = "request"
	MessageTypeResponse = "response"
	MessageTypeRegister = "register"
	// MessageTypeRegisterResult answers a register that carried an attach
	// document (runner-attach §6).
	MessageTypeRegisterResult = "register_result"
)

type TunnelMessage struct {
	ResponseMode string `json:"response_mode,omitempty"`
	Seq          uint64 `json:"seq,omitempty"`
	Type         string `json:"type"`
	ID           string `json:"id"`
	// Body carries a runner attach document on a register frame and the
	// register_result on the reply (runner-attach §2, §6). Absent on the
	// legacy backend-ids register.
	Body       json.RawMessage     `json:"body,omitempty"`
	Method     string              `json:"method,omitempty"`
	URL        string              `json:"url,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	BodyBase64 string              `json:"body_base64,omitempty"`
	StatusCode int                 `json:"status_code,omitempty"`
	// Trailers are the runner's HTTP trailers on a response frame: the
	// usage claim of a streamed paid job (paid-job §3.2), which the
	// response-trailer extractor reads. Absent on frames from an agent
	// that predates the field.
	Trailers map[string][]string `json:"trailers,omitempty"`
	Error    string              `json:"error,omitempty"`
}

type SessionForwarder struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  uint64
	pending map[string]chan TunnelMessage
	closed  bool
	done    chan struct{}
	// onRegister receives register frames the runner sends after the
	// first one (a re-sent attach document, runner-attach §2).
	onRegister func(TunnelMessage)
}

// SetRegisterHandler installs the callback for re-sent register frames.
func (s *SessionForwarder) SetRegisterHandler(fn func(TunnelMessage)) {
	s.mu.Lock()
	s.onRegister = fn
	s.mu.Unlock()
}

// SendMessage writes a broker→runner frame outside the request path
// (register_result).
func (s *SessionForwarder) SendMessage(msg TunnelMessage) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return s.conn.WriteJSON(msg)
}

// ReadRegister reads exactly one frame and requires it to be a register.
// Used once, before the read loop starts, on the attach path.
func ReadRegister(conn *websocket.Conn, timeout time.Duration) (TunnelMessage, error) {
	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	var msg TunnelMessage
	if err := conn.ReadJSON(&msg); err != nil {
		return TunnelMessage{}, err
	}
	if msg.Type != MessageTypeRegister {
		return TunnelMessage{}, fmt.Errorf("expected register message first, got %q", msg.Type)
	}
	return msg, nil
}

func NewSessionForwarder(conn *websocket.Conn) *SessionForwarder {
	s := NewSessionForwarderDeferred(conn)
	s.Start()
	return s
}

// NewSessionForwarderDeferred constructs without starting the read
// loop, so the attach path can read the first register frame itself.
func NewSessionForwarderDeferred(conn *websocket.Conn) *SessionForwarder {
	// Bound even legacy full-body frames (64 MiB decoded plus base64/metadata).
	conn.SetReadLimit((64<<20)*4/3 + (1 << 20))
	return &SessionForwarder{
		conn:    conn,
		pending: make(map[string]chan TunnelMessage),
		done:    make(chan struct{}),
	}
}

// Start begins the read loop.
func (s *SessionForwarder) Start() { go s.readLoop() }

func (s *SessionForwarder) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.done)
	s.pending = make(map[string]chan TunnelMessage)
	s.mu.Unlock()
	return s.conn.Close()
}

func (s *SessionForwarder) Done() <-chan struct{} {
	return s.done
}

func (s *SessionForwarder) Forward(ctx context.Context, req backend.ForwardRequest) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		var err error
		body, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
	}
	id, ch, err := s.registerPending()
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			s.unregisterPending(id)
			_ = s.SendMessage(TunnelMessage{Type: "request_cancel", ID: id})
		}
	}()
	msg := TunnelMessage{
		ResponseMode: "chunks-v1",
		Type:         MessageTypeRequest,
		ID:           id,
		Method:       defaultMethod(req.Method),
		URL:          req.URL,
		Headers:      headerMap(req.Headers),
		BodyBase64:   base64.StdEncoding.EncodeToString(body),
	}
	err = s.SendMessage(msg)
	if err != nil {
		return nil, err
	}
	resp, err := s.receive(ctx, ch)
	if err != nil {
		return nil, err
	}
	if resp.Type == "response_start" {
		if resp.StatusCode < 200 || resp.StatusCode > 599 {
			return nil, fmt.Errorf("invalid streaming response status")
		}
		header := http.Header(resp.Headers)
		if header == nil {
			header = make(http.Header)
		}
		header.Del("Content-Length")
		header.Del("Transfer-Encoding")
		out := &http.Response{StatusCode: resp.StatusCode, Header: header, ContentLength: -1, Trailer: make(http.Header)}
		bodyCtx, cancelBody := context.WithCancel(ctx)
		b := &responseStream{ctx: bodyCtx, cancel: cancelBody, session: s, id: id, messages: ch, trailers: out.Trailer, stopped: make(chan struct{})}
		out.Body = b
		keep = true
		go func() {
			select {
			case <-ctx.Done():
				b.Close()
			case <-s.done:
				return
			case <-b.stopped:
			}
		}()
		return out, nil
	}
	if resp.Type != MessageTypeResponse && resp.Type != "response_error" {
		return nil, fmt.Errorf("unexpected response frame %s", resp.Type)
	}
	if resp.Type == "response_error" || resp.Error != "" {
		return nil, fmt.Errorf("worker request failed: %s", resp.Error)
	}
	respBody, err := base64.StdEncoding.DecodeString(resp.BodyBase64)
	if err != nil {
		return nil, fmt.Errorf("decode worker response body: %w", err)
	}
	header := http.Header(resp.Headers)
	if header == nil {
		// A runner may answer with no headers at all; the response
		// still has to be writable.
		header = http.Header{}
	}
	out := &http.Response{
		StatusCode: resp.StatusCode,
		Header:     header,
		Body:       io.NopCloser(bytes.NewReader(respBody)),
	}
	if len(resp.Trailers) > 0 {
		out.Trailer = http.Header(resp.Trailers)
	}
	// The tunnel already holds the whole body, so the broker knows
	// the exact length. Leaving ContentLength at zero means
	// "unknown", which sends the gateway a chunked reply for a
	// response that was never streamed — and made length-delimited
	// delivery depend on every runner remembering to relay a
	// Content-Length header it should not have to think about.
	if len(out.Header.Values("Transfer-Encoding")) == 0 {
		out.ContentLength = int64(len(respBody))
		out.Header.Set("Content-Length", strconv.Itoa(len(respBody)))
	}
	return out, nil
}

// Drain frames already received before reporting connection loss. In particular,
// a valid response_end remains authoritative when a peer disconnects afterward.
func (s *SessionForwarder) receive(ctx context.Context, ch <-chan TunnelMessage) (TunnelMessage, error) {
	if err := ctx.Err(); err != nil {
		return TunnelMessage{}, err
	}
	select {
	case msg := <-ch:
		return msg, nil
	default:
	}
	select {
	case msg := <-ch:
		return msg, nil
	case <-ctx.Done():
		return TunnelMessage{}, ctx.Err()
	case <-s.done:
		select {
		case msg := <-ch:
			return msg, nil
		default:
			return TunnelMessage{}, io.ErrUnexpectedEOF
		}
	}
}

func (s *SessionForwarder) registerPending() (string, chan TunnelMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", nil, fmt.Errorf("worker session closed")
	}
	s.nextID++
	id := fmt.Sprintf("req-%d-%d", time.Now().UnixNano(), s.nextID)
	ch := make(chan TunnelMessage, 10)
	s.pending[id] = ch
	return id, ch, nil
}

func (s *SessionForwarder) unregisterPending(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, id)
}

func (s *SessionForwarder) readLoop() {
	defer func() { _ = s.Close() }()
	for {
		var msg TunnelMessage
		if err := s.conn.ReadJSON(&msg); err != nil {
			return
		}
		if msg.Type == "response_chunk" && len(msg.BodyBase64) > 43692 {
			return
		}
		if msg.Type == MessageTypeRegister {
			s.mu.Lock()
			fn := s.onRegister
			s.mu.Unlock()
			if fn != nil {
				fn(msg)
			}
			continue
		}
		if msg.Type != MessageTypeResponse && msg.Type != "response_start" && msg.Type != "response_chunk" && msg.Type != "response_end" && msg.Type != "response_error" {
			continue
		}
		s.mu.Lock()
		ch := s.pending[msg.ID]
		s.mu.Unlock()
		if ch != nil {
			// Never let one slow consumer block the shared reader. A peer
			// respecting the eight-chunk credit cannot overflow this queue.
			select {
			case ch <- msg:
			default:
				return
			}
		}
	}
}

func headerMap(h http.Header) map[string][]string {
	if h == nil {
		return nil
	}
	out := make(map[string][]string, len(h))
	for k, values := range h {
		out[k] = append([]string(nil), values...)
	}
	return out
}

func defaultMethod(method string) string {
	if method == "" {
		return http.MethodPost
	}
	return method
}

func ReadTunnelMessage(r io.Reader) (TunnelMessage, error) {
	var msg TunnelMessage
	err := json.NewDecoder(r).Decode(&msg)
	return msg, err
}

var _ backend.Forwarder = (*SessionForwarder)(nil)
