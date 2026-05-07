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
	s.mux.HandleFunc("GET /registry/health", registry.HealthHandler(s.cfg))
	s.mux.HandleFunc("GET /healthz", registry.HealthzHandler())

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
