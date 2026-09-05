package server

import (
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/certification"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/registry"
)

// registerRoutes wires the broker's unpaid surfaces: registry, health,
// admin, ticket-params, and runner attach endpoints.
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
	// Settlement serves BOTH protocols, so it is registered here rather
	// than with the session routes: a job-only broker — what an
	// OpenAI-style gateway runs — needs it most, because a streamed
	// job's claim arrives in a trailer its SDK may not be able to read.
	s.mux.HandleFunc("GET /v1/settlement/{id}", s.handleSettlement)
	// Evidence of ABSENCE, keyed on the id the consumer issued so it is
	// retrievable without anything the customer holds.
	s.mux.HandleFunc("POST /v1/non-admission/{request_id}", s.handleNonAdmission)
	// What happened to this request, keyed on the id the consumer
	// issued. Every other lookup here is keyed on something the customer
	// holds, so a customer that withheld the settlement could force a
	// conservative full charge the broker had evidence against.
	s.mux.HandleFunc("GET /v1/exchange/{request_id}", s.handleExchangeByRequestID)
	s.mux.HandleFunc("POST /v1/payment/ticket-params", ticketParamsHandler(s.payment))
	s.mux.HandleFunc("GET /admin/v1/runtime", s.handleRuntimeStatus)
	s.mux.HandleFunc("POST /admin/v1/runtime/reload", s.handleRuntimeReload)
	// The runner attach endpoint. The path keeps its old spelling on
	// purpose: it is in every bundle this broker has minted and in
	// every agent already running, and renaming a wire path to match a
	// deleted feature's departure would strand them all.
	s.mux.HandleFunc("GET /internal/v1/worker/session", s.handleAttachWS)
	// Usage callback for a session under certification (certification-steps §3.3).
	s.mux.HandleFunc("POST "+certification.TapPathPrefix+"{tap_id}", s.handleCertificationUsage)
	// Run-scoped fixture source and output sink for runners that fetch
	// their input and write their output (certification-steps §4).
	s.mux.HandleFunc("GET "+certification.FixturePathPrefix+"{scope}/{ref...}", s.handleCertificationFixture)
	s.mux.HandleFunc("PUT "+certification.SinkPathPrefix+"{scope}", s.handleCertificationSink)
	s.mux.HandleFunc("POST "+certification.SinkPathPrefix+"{scope}", s.handleCertificationSink)
	// A runner writing several artifacts — an ABR ladder is a manifest,
	// a playlist and a media file per rendition — names each under the
	// scope. The path is the runner's; the sink counts and discards.
	s.mux.HandleFunc("PUT "+certification.SinkPathPrefix+"{scope}/{artifact...}", s.handleCertificationSink)
	s.mux.HandleFunc("POST "+certification.SinkPathPrefix+"{scope}/{artifact...}", s.handleCertificationSink)
	// Offers (broker-admin §4).
	s.mux.HandleFunc("GET /admin/v1/offers", s.handleOffersList)
	s.mux.HandleFunc("PUT /admin/v1/offers", s.handleOffersPut)
	s.mux.HandleFunc("GET /admin/v1/offers/{offering_id}", s.handleOfferGet)
	s.mux.HandleFunc("POST /admin/v1/offers/{offering_id}/accept-shape", s.handleOfferAcceptShape)
	s.mux.HandleFunc("POST /admin/v1/offers/{offering_id}/confirm-published", s.handleOfferConfirmPublished)
	s.mux.HandleFunc("POST /admin/v1/offers/{offering_id}/disable", s.handleOfferDisable)
	s.mux.HandleFunc("POST /admin/v1/offers/{offering_id}/enable", s.handleOfferEnable)
	// Certification (broker-admin §6).
	s.mux.HandleFunc("GET /admin/v1/certification", s.handleCertificationList)
	s.mux.HandleFunc("GET /admin/v1/certification/{host_id}/{offering_id}", s.handleCertificationPair)
	s.mux.HandleFunc("POST /admin/v1/certification/{host_id}/{offering_id}/run", s.handleCertificationRun)
	// Attached runners (broker-admin §3).
	s.mux.HandleFunc("GET /admin/v1/runners", s.handleRunnersList)
	s.mux.HandleFunc("GET /admin/v1/runners/{host_id}", s.handleRunnerGet)
	s.mux.HandleFunc("POST /admin/v1/runners/{host_id}/disconnect", s.handleRunnerDisconnect)
	// Credential store (broker-admin §5). 404 when no store is configured.
	s.mux.HandleFunc("POST /admin/v1/enroll", s.handleEnroll)
	s.mux.HandleFunc("GET /admin/v1/credentials", s.handleCredentialsList)
	s.mux.HandleFunc("PUT /admin/v1/credentials", s.handleCredentialsSync)
	s.mux.HandleFunc("GET /admin/v1/credentials/{credential_id}", s.handleCredentialGet)
	s.mux.HandleFunc("POST /admin/v1/credentials/{credential_id}/rotate", s.handleCredentialRotate)
	s.mux.HandleFunc("POST /admin/v1/credentials/{credential_id}/revoke", s.handleCredentialRevoke)

	// Metrics live on a separate listener (cfg.Listen.Metrics, default :9090);
	// see metrics_server.go. This intentionally does NOT register /metrics on
	// the paid listener — scrapes shouldn't traverse the paid middleware chain.
}
