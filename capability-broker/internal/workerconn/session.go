package workerconn

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/backend"
	"github.com/gorilla/websocket"
)

const (
	MessageTypeRequest  = "request"
	MessageTypeResponse = "response"
	MessageTypeRegister = "register"
)

type TunnelMessage struct {
	Type       string              `json:"type"`
	ID         string              `json:"id"`
	Method     string              `json:"method,omitempty"`
	URL        string              `json:"url,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	BodyBase64 string              `json:"body_base64,omitempty"`
	StatusCode int                 `json:"status_code,omitempty"`
	Error      string              `json:"error,omitempty"`
}

type SessionForwarder struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  uint64
	pending map[string]chan TunnelMessage
	closed  bool
	done    chan struct{}
}

func NewSessionForwarder(conn *websocket.Conn) *SessionForwarder {
	s := &SessionForwarder{
		conn:    conn,
		pending: make(map[string]chan TunnelMessage),
		done:    make(chan struct{}),
	}
	go s.readLoop()
	return s
}

func (s *SessionForwarder) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.done)
	pending := s.pending
	s.pending = make(map[string]chan TunnelMessage)
	s.mu.Unlock()
	for _, ch := range pending {
		close(ch)
	}
	return s.conn.Close()
}

func (s *SessionForwarder) Done() <-chan struct{} {
	return s.done
}

func (s *SessionForwarder) Forward(ctx context.Context, req backend.ForwardRequest) (*http.Response, error) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}
	id, ch, err := s.registerPending()
	if err != nil {
		return nil, err
	}
	defer s.unregisterPending(id)
	msg := TunnelMessage{
		Type:       MessageTypeRequest,
		ID:         id,
		Method:     defaultMethod(req.Method),
		URL:        req.URL,
		Headers:    headerMap(req.Headers),
		BodyBase64: base64.StdEncoding.EncodeToString(body),
	}
	s.writeMu.Lock()
	err = s.conn.WriteJSON(msg)
	s.writeMu.Unlock()
	if err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return nil, fmt.Errorf("worker session closed")
		}
		if resp.Error != "" {
			return nil, fmt.Errorf("worker request failed: %s", resp.Error)
		}
		respBody, err := base64.StdEncoding.DecodeString(resp.BodyBase64)
		if err != nil {
			return nil, fmt.Errorf("decode worker response body: %w", err)
		}
		return &http.Response{
			StatusCode: resp.StatusCode,
			Header:     http.Header(resp.Headers),
			Body:       io.NopCloser(bytes.NewReader(respBody)),
		}, nil
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
	ch := make(chan TunnelMessage, 1)
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
		if msg.Type != MessageTypeResponse {
			continue
		}
		s.mu.Lock()
		ch := s.pending[msg.ID]
		s.mu.Unlock()
		if ch != nil {
			ch <- msg
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
