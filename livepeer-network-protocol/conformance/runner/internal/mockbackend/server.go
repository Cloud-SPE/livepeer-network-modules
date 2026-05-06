// Package mockbackend provides an in-process HTTP server the runner uses as
// the target for broker forwarding during a fixture run. It's programmable
// per-fixture (status / headers / body) and records inbound calls for
// backend_assertions verification.
package mockbackend

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Response programs what the mock backend returns on the next call.
type Response struct {
	Status  int
	Headers map[string]string
	Body    string
}

// Call records one inbound call to the mock backend.
type Call struct {
	Method  string
	Path    string
	Headers http.Header
	Body    string
}

// Server is a programmable HTTP server. It returns the configured Response
// for every request and records the inbound Call. /ws upgrades to a
// WebSocket and echoes any text/binary frame back.
type Server struct {
	addr     string
	srv      *http.Server
	upgrader websocket.Upgrader

	mu       sync.Mutex
	response Response
	calls    []Call
}

// New returns a Server bound to addr (e.g. ":9000"). Call Run() to start;
// Set/Reset/LastCall to drive it.
//
// The server handles two paths:
//   - / (everything else)  — programmable HTTP responses (Set/Reset).
//   - /ws (WebSocket)      — echoes any text/binary frame back. Records
//                            the upgrade headers in Calls() so backend
//                            assertions can verify Livepeer-* stripping.
func New(addr string) *Server {
	s := &Server{
		addr:     addr,
		response: Response{Status: 200, Body: "{}\n"},
		upgrader: websocket.Upgrader{
			HandshakeTimeout: 10 * time.Second,
			CheckOrigin:      func(r *http.Request) bool { return true },
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", s.wsHandler)
	mux.HandleFunc("/", s.handler)
	s.srv = &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

// Run starts the listener; blocks until Stop or error. Call as a goroutine.
func (s *Server) Run() error {
	if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Stop closes the listener and shuts the server down.
func (s *Server) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.srv.Shutdown(ctx)
}

// Set replaces the programmed Response.
func (s *Server) Set(resp Response) {
	s.mu.Lock()
	s.response = resp
	if s.response.Status == 0 {
		s.response.Status = http.StatusOK
	}
	s.mu.Unlock()
}

// Reset clears recorded calls.
func (s *Server) Reset() {
	s.mu.Lock()
	s.calls = nil
	s.mu.Unlock()
}

// LastCall returns the most recently recorded call, or nil if none.
func (s *Server) LastCall() *Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.calls) == 0 {
		return nil
	}
	c := s.calls[len(s.calls)-1]
	return &c
}

// Calls returns a snapshot of all recorded calls.
func (s *Server) Calls() []Call {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Call, len(s.calls))
	copy(out, s.calls)
	return out
}

func (s *Server) handler(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.calls = append(s.calls, Call{
		Method:  r.Method,
		Path:    r.URL.Path,
		Headers: r.Header.Clone(),
		Body:    string(body),
	})
	resp := s.response
	s.mu.Unlock()

	for k, v := range resp.Headers {
		w.Header().Set(k, v)
	}
	if resp.Status == 0 {
		resp.Status = http.StatusOK
	}
	w.WriteHeader(resp.Status)
	_, _ = w.Write([]byte(resp.Body))
}

// wsHandler accepts a WebSocket upgrade and echoes any frame back.
// Records the upgrade headers in Calls so backend assertions can verify
// Livepeer-* stripping.
func (s *Server) wsHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.calls = append(s.calls, Call{
		Method:  r.Method,
		Path:    r.URL.Path,
		Headers: r.Header.Clone(),
	})
	s.mu.Unlock()

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade has written the error response.
		return
	}
	defer conn.Close()

	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if err := conn.WriteMessage(mt, data); err != nil {
			return
		}
	}
}
