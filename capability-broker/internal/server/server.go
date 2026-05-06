// Package server wires the HTTP server, route table, and middleware chain.
package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
)

// Server wraps the broker's HTTP server. It owns its paid listener; metrics
// will be served on a separate listener once observability lands (plan 0003
// polish commit).
type Server struct {
	cfg *config.Config
	mux *http.ServeMux
	srv *http.Server
}

// New constructs a Server from a validated config and registers routes.
// Call Run to start.
func New(cfg *config.Config) *Server {
	mux := http.NewServeMux()
	srv := &http.Server{
		Addr:              cfg.Listen.Paid,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	s := &Server{cfg: cfg, mux: mux, srv: srv}
	s.registerRoutes()
	return s
}

// Run starts the server in the foreground. Blocks until ctx is canceled or
// the server errors; performs a graceful shutdown on cancellation.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		log.Printf("listening on %s (paid)", s.cfg.Listen.Paid)
		if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("listen: %w", err)
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-errCh:
		return err
	}
}
