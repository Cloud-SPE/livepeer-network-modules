package server

import (
	"net/http"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/server/middleware"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/server/registry"
)

func (s *Server) registerRoutes() {
	// Unpaid registry endpoints — no Livepeer-* validation, no payment lifecycle.
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
	// Order: outermost first; recover wraps everything to catch panics.
	paidChain := middleware.Chain(
		middleware.Recover,
		middleware.RequestID,
		middleware.Headers,
		middleware.Payment,
	)

	// POST /v1/cap — http-reqresp / http-stream / http-multipart dispatch.
	s.mux.Handle("POST /v1/cap", paidChain(http.HandlerFunc(s.todoModeDispatch)))

	// GET /v1/cap — ws-realtime upgrade.
	s.mux.Handle("GET /v1/cap", paidChain(http.HandlerFunc(s.todoWebSocketUpgrade)))
}

// todoModeDispatch is a placeholder until internal/modes/* drivers are wired
// (plan 0003 dispatch commit).
func (s *Server) todoModeDispatch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(livepeerheader.Error, livepeerheader.ErrInternalError)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = w.Write([]byte(`{"error":"internal_error","message":"mode dispatch not implemented in v0.1 scaffold; see plan 0003"}` + "\n"))
}

// todoWebSocketUpgrade is a placeholder until ws-realtime lands (plan 0006).
func (s *Server) todoWebSocketUpgrade(w http.ResponseWriter, r *http.Request) {
	w.Header().Set(livepeerheader.Error, livepeerheader.ErrModeUnsupported)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_, _ = w.Write([]byte(`{"error":"mode_unsupported","message":"ws-realtime not implemented in v0.1 scaffold; see plan 0006"}` + "\n"))
}
