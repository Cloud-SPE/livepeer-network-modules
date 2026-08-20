package server

import (
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/registry"
)

// registerRoutes wires the broker's unpaid surfaces: registry, health,
// admin, ticket-params, and worker-session endpoints.
//
// The paid dispatch surface (POST/GET /v1/cap and friends) was removed
// with the v0 interaction-mode taxonomy. The paid routes now belong to
// the two protocol engines, which register them separately:
// registerJobRoutes (POST /v1/job, job_routes.go) and
// registerSessionRoutes (/v1/session/*, session_routes.go).
func (s *Server) registerRoutes() {
	// Unpaid registry endpoints — no Livepeer-* validation, no payment.
	s.mux.HandleFunc("GET /registry/offerings", instrumentRegistryScrape("offerings", s.handleOfferings))
	s.mux.HandleFunc("GET /registry/health", instrumentRegistryScrape("health", s.handleRegistryHealth))
	s.mux.HandleFunc("GET /healthz", registry.HealthzHandler())
	s.mux.HandleFunc("POST /v1/payment/ticket-params", ticketParamsHandler(s.payment))
	s.mux.HandleFunc("GET /admin/v1/runtime", s.handleRuntimeStatus)
	s.mux.HandleFunc("POST /admin/v1/runtime/reload", s.handleRuntimeReload)
	s.mux.HandleFunc("GET /admin/v1/worker-sessions", s.handleWorkerSessions)
	s.mux.HandleFunc("POST /admin/v1/worker-sessions/{backend_id}/kill", s.handleWorkerSessionKill)
	s.mux.HandleFunc("GET /internal/v1/worker/session", s.handleWorkerSession)

	// Metrics live on a separate listener (cfg.Listen.Metrics, default :9090);
	// see metrics_server.go. This intentionally does NOT register /metrics on
	// the paid listener — scrapes shouldn't traverse the paid middleware chain.
}
