package server

import (
	"net/http"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/server/middleware"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/server/registry"
)

func (s *Server) registerRoutes() {
	// Unpaid registry endpoints — no Livepeer-* validation, no payment.
	s.mux.HandleFunc("GET /registry/offerings", registry.OfferingsHandler(s.cfg))
	s.mux.HandleFunc("GET /registry/health", registry.HealthHandler(s.cfg))
	s.mux.HandleFunc("GET /healthz", registry.HealthzHandler())

	// Metrics endpoint — TODO: real Prometheus collector wired in plan 0003 polish commit.
	s.mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# TODO: Prometheus metrics not yet wired (plan 0003 polish commit)\n"))
	})

	// Paid mode-dispatch endpoints share a middleware chain.
	// Order: outermost first; Recover wraps everything to catch panics.
	paidChain := middleware.Chain(
		middleware.Recover,
		middleware.RequestID,
		middleware.Headers,
		middleware.Payment(s.payment),
	)

	// POST /v1/cap — http-reqresp / http-stream / http-multipart dispatch.
	s.mux.Handle("POST /v1/cap", paidChain(http.HandlerFunc(s.dispatch)))

	// GET /v1/cap — ws-realtime upgrade (driver lands in plan 0006).
	s.mux.Handle("GET /v1/cap", paidChain(http.HandlerFunc(s.todoWebSocketUpgrade)))
}

// todoWebSocketUpgrade is a placeholder until ws-realtime lands (plan 0006).
func (s *Server) todoWebSocketUpgrade(w http.ResponseWriter, r *http.Request) {
	livepeerheader.WriteError(w, http.StatusNotImplemented, livepeerheader.ErrModeUnsupported,
		"ws-realtime not implemented in v0.1; see plan 0006")
}
