// Package publicapi is the resolver-facing HTTP listener. It serves
// EXACTLY ONE path:
//
//	GET /.well-known/livepeer-registry.json
//
// All other paths return 404 — defense-in-depth lockdown so a routing
// bug elsewhere in the codebase cannot accidentally expose admin or
// operator-UX routes via the public listener (plan 0018 §11).
package publicapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/published"
)

// WellKnownPath is the manifest URL resolvers fetch.
const WellKnownPath = "/.well-known/livepeer-registry.json"

// Server is the public listener.
type Server struct {
	addr   string
	store  *published.Store
	logger *slog.Logger

	mu       sync.Mutex
	listener net.Listener
	httpSrv  *http.Server
}

// New builds a Server.
func New(addr string, store *published.Store, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{addr: addr, store: store, logger: logger}
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
		return nil, fmt.Errorf("publicapi: listen %s: %w", s.addr, err)
	}
	s.listener = ln
	s.httpSrv = &http.Server{
		Handler:           s.lockedDownHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return ln.Addr(), nil
}

// Serve runs until ctx cancellation.
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

// Addr returns the bound address.
func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}

func (s *Server) lockedDownHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != WellKnownPath {
			http.NotFound(w, r)
			return
		}
		body, mod, err := s.store.Read()
		if err != nil {
			if errors.Is(err, published.ErrEmpty) {
				http.Error(w, "no manifest published", http.StatusServiceUnavailable)
				return
			}
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache, must-revalidate")
		if !mod.IsZero() {
			w.Header().Set("Last-Modified", mod.UTC().Format(http.TimeFormat))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
}
