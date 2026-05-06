package server

import (
	"net/http"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/backend"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/modes"
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

	_ = driver.Serve(r.Context(), modes.Params{
		Writer:     w,
		Request:    r,
		Capability: cap,
		Extractor:  extractor,
		Backend:    s.backend,
		Auth:       backend.NewAuthApplier(s.secrets),
	})
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
