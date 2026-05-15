package server

import (
	"math/big"
	"net/http"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/media/hls"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/server/middleware"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/server/registry"
)

func (s *Server) registerRoutes() {
	// Unpaid registry endpoints — no Livepeer-* validation, no payment.
	s.mux.HandleFunc("GET /registry/offerings", registry.OfferingsHandler(s.cfg))
	s.mux.HandleFunc("GET /registry/health", registry.HealthHandler(s.health))
	s.mux.HandleFunc("GET /healthz", registry.HealthzHandler())
	s.mux.HandleFunc("POST /v1/payment/ticket-params", ticketParamsHandler(s.payment))

	// LL-HLS playback served from the per-session scratch. The URL is
	// itself a per-session bearer secret (12 random bytes hex) so
	// playback isn't gated by the payment middleware — that ran at
	// session-open. Registered only when the scratch dir is set.
	if s.opts.HLS.ScratchDir != "" {
		s.mux.Handle("/_hls/", hls.Handler(s.opts.HLS.ScratchDir, func(id string) bool {
			return s.rtmpStore != nil && s.rtmpStore.Get(id) != nil
		}))
	}

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
		middleware.Payment(s.payment, s.capabilityLookup(), s.opts.InterimDebit),
	)

	// POST /v1/cap — http-reqresp / http-stream / http-multipart /
	//                rtmp-ingress-hls-egress / session-control-plus-media
	//                (the latter two are session-open phase in v0.1).
	s.mux.Handle("POST /v1/cap", paidChain(http.HandlerFunc(s.dispatch)))

	// GET /v1/cap — ws-realtime upgrade. Same dispatcher handles the
	// (method, mode) selection; the ws-realtime driver upgrades the
	// connection in its Serve method.
	s.mux.Handle("GET /v1/cap", paidChain(http.HandlerFunc(s.dispatch)))

	// POST /v1/cap/{session_id}/end — gateway-initiated session close
	// for rtmp-ingress-hls-egress per the mode spec §"Session-end".
	// Unpaid: the session was already paid for at session-open, and
	// the URL path is a per-session bearer secret. 404 on unknown
	// session id; 204 on a successful tear-down.
	s.mux.HandleFunc("POST /v1/cap/{session_id}/end", s.rtmpCloseSession)

	// GET /v1/cap/{session_id}/control — session-control-plus-media OR
	// session-control-external-media control-WebSocket upgrade. Unpaid:
	// the URL path is the per-session bearer (Q1 lock — path-id-only
	// auth). The dispatcher routes by session-store ownership.
	if s.sessDriver != nil || s.extDriver != nil {
		s.mux.HandleFunc("GET /v1/cap/{session_id}/control", s.dispatchControlWS)
	}

	// /_scope/{session_id}/{path...} — session-control-external-media
	// reverse-proxy plane. Forwards customer (gateway) traffic to the
	// workload backend's HTTP API. Unpaid: the session id is the
	// bearer. The driver's proxy handler authorises against the live
	// session record + strips Livepeer-* headers.
	if s.extDriver != nil {
		s.mux.HandleFunc("/_scope/{session_id}/{path...}", s.extDriver.ServeProxy)
	}
}

// dispatchControlWS routes a control-WS upgrade to whichever mode owns
// the named session. If both stores claim it (cannot happen — IDs are
// 12 random bytes), the external-media driver wins. If neither owns it,
// returns 401.
func (s *Server) dispatchControlWS(w http.ResponseWriter, r *http.Request) {
	sessID := r.PathValue("session_id")
	if sessID == "" {
		http.Error(w, "missing session_id", http.StatusBadRequest)
		return
	}
	if s.extDriver != nil && s.extDriver.Store().Get(sessID) != nil {
		s.extDriver.ServeControlWS(w, r)
		return
	}
	if s.sessDriver != nil {
		s.sessDriver.ServeControlWS(w, r)
		return
	}
	http.Error(w, "session not found", http.StatusUnauthorized)
}

// capabilityLookup returns a CapabilityLookup function the payment
// middleware uses to translate (capability, offering) into the
// (work_unit, price_per_work_unit_wei) tuple the daemon needs at
// OpenSession time.
//
// Maps the broker's host-config Price (`amount_wei` per `per_units`)
// into a per-work-unit wei value. Both fields are validated upstream;
// price_per_work_unit_wei is `amount_wei / per_units`.
func (s *Server) capabilityLookup() middleware.CapabilityLookup {
	return func(capability, offering string) (middleware.CapabilitySpec, bool) {
		cap, ok := s.lookup(capability, offering)
		if !ok || cap == nil {
			return middleware.CapabilitySpec{}, false
		}
		amount, ok := new(big.Int).SetString(cap.Price.AmountWei, 10)
		if !ok {
			return middleware.CapabilitySpec{}, false
		}
		perUnits := big.NewInt(int64(cap.Price.PerUnits))
		if perUnits.Sign() == 0 {
			return middleware.CapabilitySpec{}, false
		}
		// Wei per work unit = amount_wei / per_units. Integer division
		// is fine — config validates per_units > 0 and amount_wei is a
		// non-negative decimal string.
		pricePerUnit := new(big.Int).Quo(amount, perUnits)
		return middleware.CapabilitySpec{
			WorkUnit:            cap.WorkUnit.Name,
			PricePerWorkUnitWei: pricePerUnit,
		}, true
	}
}
