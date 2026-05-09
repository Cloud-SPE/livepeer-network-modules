// Package adminapi is the operator-facing HTTP surface (web UI +
// JSON API + signed-manifest upload). Routes land progressively
// across plan 0018 commits 2–6.
package adminapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"
)

// Server bundles the admin listener with the routes the operator UX
// needs. The route surface is mux-driven; later commits register
// handlers against the same Server.
type Server struct {
	addr   string
	logger *slog.Logger
	auth   *authManager

	mu       sync.Mutex
	mux      *http.ServeMux
	listener net.Listener
	httpSrv  *http.Server
}

// New builds a Server bound to the given address. addr should be a
// LAN-private interface (the coordinator's operator UX is intended to
// be reachable on the LAN, not the public internet).
func New(addr string, logger *slog.Logger, adminTokens []string) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{addr: addr, logger: logger, auth: newAuthManager(adminTokens), mux: http.NewServeMux()}
}

// Mux returns the underlying ServeMux so callers can register
// additional routes. Returning the mux directly is intentional —
// commit 4 wires the signed-manifest upload route and commit 6 wires
// the web UI handlers.
func (s *Server) Mux() *http.ServeMux {
	return s.mux
}

// Listen binds the TCP listener.
func (s *Server) Listen() (net.Addr, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return s.listener.Addr(), nil
	}
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return nil, fmt.Errorf("adminapi: listen %s: %w", s.addr, err)
	}
	s.listener = ln
	s.httpSrv = &http.Server{
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return ln.Addr(), nil
}

// Serve runs the server until ctx is canceled.
func (s *Server) Serve(ctx context.Context) error {
	if _, err := s.Listen(); err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.httpSrv.Shutdown(shutCtx)
	}()
	if err := s.httpSrv.Serve(s.listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// Addr returns the bound address; empty before Listen.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}
