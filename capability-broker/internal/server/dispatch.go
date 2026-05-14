package server

import (
	"net/http"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors/bytescounted"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors/runnerreport"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors/secondselapsed"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/server/middleware"
)

// dispatch is the handler at POST /v1/cap. It runs *after* the middleware
// chain has validated headers and opened a payment session. Its job is to:
//
//  1. Look up the (capability_id, offering_id) in the configured catalog.
//  2. Verify Livepeer-Mode matches the capability's declared interaction_mode.
//  3. Build the per-request extractor instance.
//  4. Call the mode driver's Serve method.
//
// Errors from any step produce an appropriate Livepeer-Error response; the
// driver itself is responsible for setting Livepeer-Work-Units on success.
func (s *Server) dispatch(w http.ResponseWriter, r *http.Request) {
	capID := r.Header.Get(livepeerheader.Capability)
	offID := r.Header.Get(livepeerheader.Offering)
	mode := r.Header.Get(livepeerheader.Mode)

	cap, found := s.lookup(capID, offID)
	if cap == nil {
		if !found {
			livepeerheader.WriteError(w, http.StatusNotFound, livepeerheader.ErrCapabilityNotServed,
				"capability "+capID+" is not served by this broker")
		} else {
			livepeerheader.WriteError(w, http.StatusNotFound, livepeerheader.ErrOfferingNotServed,
				"offering "+offID+" is not served under capability "+capID)
		}
		return
	}

	if mode != cap.InteractionMode {
		livepeerheader.WriteError(w, http.StatusHTTPVersionNotSupported, livepeerheader.ErrModeUnsupported,
			"capability "+capID+"/"+offID+" expects "+cap.InteractionMode+", got "+mode)
		return
	}

	driver, ok := s.modes.Get(mode)
	if !ok {
		livepeerheader.WriteError(w, http.StatusHTTPVersionNotSupported, livepeerheader.ErrModeUnsupported,
			"mode "+mode+" is not implemented by this broker (available: "+joinNames(s.modes.Names())+")")
		return
	}

	extractor, err := s.extractors.Build(cap.WorkUnit.Extractor)
	if err != nil {
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"extractor build failed: "+err.Error())
		return
	}

	// Plan 0015: build a LiveCounter when the extractor supports the
	// running view AND the mode driver is one of the long-running
	// modes that polls it. The LiveCounter is published to the
	// payment middleware via SessionStateFromContext so the ticker
	// goroutine can read CurrentUnits.
	live := buildLiveCounter(extractor, mode)
	if live != nil {
		if state := middleware.SessionStateFromContext(r.Context()); state != nil {
			state.SetLiveCounter(live)
		}
	}

	_ = driver.Serve(r.Context(), modes.Params{
		Writer:      w,
		Request:     r,
		Capability:  cap,
		Extractor:   extractor,
		LiveCounter: live,
		Backend:     s.backend,
		Auth:        backend.NewAuthApplier(s.secrets),
	})
}

// buildLiveCounter wires the extractor's LiveCounter sibling for modes
// that consume one. Returns nil when either:
//   - the configured extractor doesn't have a LiveCounter (e.g.
//     `openai-usage`, `response-jsonpath` — no meaningful interim view); or
//   - the mode is request/response and doesn't run the interim ticker.
//
// `bytes-counted` returns a *bytescounted.LiveCounter which long-running
// drivers (`ws-realtime`, eventually `http-stream`) type-assert back to
// call AddBytes from their proxy loop.
//
// `seconds-elapsed` returns a closure over time.Now anchored at the
// dispatch instant; the driver doesn't need to hold a reference.
func buildLiveCounter(ext extractors.Extractor, mode string) extractors.LiveCounter {
	if !modeSupportsInterimDebit(mode) {
		return nil
	}
	switch e := ext.(type) {
	case *bytescounted.Extractor:
		return e.NewLiveCounter()
	case *secondselapsed.Extractor:
		return e.NewLiveCounter(time.Now())
	case *runnerreport.Extractor:
		return e.NewLiveCounter()
	}
	return nil
}

// modeSupportsInterimDebit returns true when the mode name carries a
// long-running session shape. Plan 0015 §8 mode-by-mode applicability.
//
// The HTTP request/response modes (`http-reqresp`, `http-multipart`)
// finish in one round-trip; the broker's existing single-debit at
// handler completion is correct. `http-stream` is conditional —
// streaming responses with a bytes/seconds extractor want interim
// debit, but v0.1 of plan 0015 ships ws-realtime only. The two
// session-open modes (`rtmp-ingress-hls-egress`,
// `session-control-plus-media`) gain interim debit when their media-
// plane followup plans land.
func modeSupportsInterimDebit(mode string) bool {
	switch mode {
	case "ws-realtime@v0":
		return true
	case "session-control-plus-media@v0":
		return true
	case "session-control-external-media@v0":
		return true
	case "rtmp-ingress-hls-egress@v0":
		return true
	default:
		return false
	}
}

// lookup returns the matching capability and a "found capability_id at all"
// boolean (used to distinguish capability_not_served from offering_not_served).
func (s *Server) lookup(capID, offID string) (*config.Capability, bool) {
	if capID == "" {
		return nil, false
	}
	var anyOffering bool
	for i := range s.cfg.Capabilities {
		c := &s.cfg.Capabilities[i]
		if c.ID != capID {
			continue
		}
		anyOffering = true
		if c.OfferingID == offID {
			return c, true
		}
	}
	return nil, anyOffering
}

// silence "declared but not used"
var (
	_ = extractors.Extractor(nil)
)

func joinNames(names []string) string {
	out := ""
	for i, n := range names {
		if i > 0 {
			out += ", "
		}
		out += n
	}
	return out
}
