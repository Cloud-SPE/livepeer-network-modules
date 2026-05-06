// Package server wires the HTTP server, route table, and middleware chain.
package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/payment"
)

// Server wraps the broker's HTTP server. It owns two listeners: the paid
// listener (cfg.Listen.Paid) for /v1/cap and /registry/*, and a metrics
// listener (cfg.Listen.Metrics) for Prometheus scraping.
type Server struct {
	cfg        *config.Config
	mux        *http.ServeMux
	srv        *http.Server
	metricsSrv *http.Server
	payment    payment.Client
	modes      *modes.Registry
	extractors *extractors.Registry
	backend    backend.Forwarder
	secrets    backend.SecretResolver
}

// New constructs a Server from a validated config and registers routes. It
// fails-fast if any configured capability references an unregistered mode or
// extractor, since those would be unservable at runtime.
func New(cfg *config.Config) (*Server, error) {
	mux := http.NewServeMux()
	srv := &http.Server{
		Addr:              cfg.Listen.Paid,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	s := &Server{
		cfg:        cfg,
		mux:        mux,
		srv:        srv,
		payment:    payment.NewMock(),
		modes:      defaultModes(),
		extractors: defaultExtractors(),
		backend:    backend.NewHTTPClient(),
		secrets:    backend.NewEnvSecretResolver(),
	}

	if err := s.validateAgainstRegistries(); err != nil {
		return nil, err
	}

	s.registerRoutes()
	s.metricsSrv = newMetricsServer(cfg.Listen.Metrics)
	return s, nil
}

// validateAgainstRegistries fails-fast if any configured capability
// references an unregistered mode or extractor.
func (s *Server) validateAgainstRegistries() error {
	for i := range s.cfg.Capabilities {
		c := &s.cfg.Capabilities[i]
		if !s.modes.Has(c.InteractionMode) {
			return fmt.Errorf("capability %s/%s: interaction_mode %q is not implemented by this broker (registered: %v)",
				c.ID, c.OfferingID, c.InteractionMode, s.modes.Names())
		}
		extractorType, _ := c.WorkUnit.Extractor["type"].(string)
		if !s.extractors.Has(extractorType) {
			return fmt.Errorf("capability %s/%s: work_unit.extractor.type %q is not implemented by this broker (registered: %v)",
				c.ID, c.OfferingID, extractorType, s.extractors.Names())
		}
	}
	return nil
}

// Run starts the server in the foreground. Blocks until ctx is canceled or
// either listener errors; performs graceful shutdown on cancellation.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 2)
	go func() {
		log.Printf("listening on %s (paid)", s.cfg.Listen.Paid)
		if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("listen paid: %w", err)
			return
		}
		errCh <- nil
	}()
	go func() {
		log.Printf("listening on %s (metrics)", s.cfg.Listen.Metrics)
		if err := s.metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("listen metrics: %w", err)
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutdownCtx)
		_ = s.metricsSrv.Shutdown(shutdownCtx)
		return ctx.Err()
	case err := <-errCh:
		_ = s.srv.Close()
		_ = s.metricsSrv.Close()
		return err
	}
}
