package server

import (
	"net/http"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/server/middleware"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/server/registry"
)

func (s *Server) registerRoutes() {
	// Unpaid registry endpoints — no Livepeer-* validation, no payment.
	s.mux.HandleFunc("GET /registry/offerings", registry.OfferingsHandler(s.cfg))
	s.mux.HandleFunc("GET /registry/health", registry.HealthHandler(s.cfg))
	s.mux.HandleFunc("GET /healthz", registry.HealthzHandler())

	// Metrics live on a separate listener (cfg.Listen.Metrics, default :9090);
	// see metrics_server.go. This intentionally does NOT register /metrics on
	// the paid listener — scrapes shouldn't traverse the paid middleware chain.

	// Paid mode-dispatch endpoints share a middleware chain.
	// Order: outermost first; Recover wraps everything to catch panics.
	paidChain := middleware.Chain(
		middleware.Recover,
		middleware.RequestID,
		middleware.Metrics,
		middleware.Headers,
		middleware.Payment(s.payment),
	)

	// POST /v1/cap — http-reqresp / http-stream / http-multipart /
	//                rtmp-ingress-hls-egress / session-control-plus-media
	//                (the latter two are session-open phase in v0.1).
	s.mux.Handle("POST /v1/cap", paidChain(http.HandlerFunc(s.dispatch)))

	// GET /v1/cap — ws-realtime upgrade. Same dispatcher handles the
	// (method, mode) selection; the ws-realtime driver upgrades the
	// connection in its Serve method.
	s.mux.Handle("GET /v1/cap", paidChain(http.HandlerFunc(s.dispatch)))
}
