package server

import (
	"net/http"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/certification"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/offers"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runnerattach"
)

// Certification admin surface (protocols/broker-admin.md §6).

func (s *Server) handleCertificationList(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	q := r.URL.Query()
	results := s.certEngine.Results(q.Get("host_id"), q.Get("offering_id"), q.Get("state"), q.Get("latest") == "true")
	if results == nil {
		results = []certification.Result{}
	}
	adminJSON(w, http.StatusOK, map[string]any{"results": results, "next_cursor": nil})
}

func (s *Server) handleCertificationPair(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	results := s.certEngine.PairResults(r.PathValue("host_id"), r.PathValue("offering_id"))
	if results == nil {
		results = []certification.Result{}
	}
	adminJSON(w, http.StatusOK, map[string]any{"results": results})
}

// handleCertificationRun starts an operator-triggered run
// (broker-admin §6.3). The matched pair supplies the capability; a pair
// that is not matched is a 409.
func (s *Server) handleCertificationRun(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdminAuth(w, r) {
		return
	}
	hostID, offeringID := r.PathValue("host_id"), r.PathValue("offering_id")
	var body struct {
		LocalID string `json:"local_id"`
	}
	if !decodeAdminBody(w, r, &body) {
		return
	}
	v, ok := s.offersEngine.ViewOf(offeringID)
	if !ok {
		adminError(w, http.StatusNotFound, "offer_not_found", "no such offering_id")
		return
	}
	sn, ok := s.runners.Get(hostID)
	if !ok {
		adminError(w, http.StatusNotFound, "runner_not_found", "no such runner within retention")
		return
	}
	// Find the matched capability: explicit local_id, or the single one
	// with a pair against this offer.
	var target *runnerattach.Capability
	matchedCount := 0
	for _, cv := range sn.Capabilities {
		if cv.Capability == nil {
			continue
		}
		pairs := s.offersEngine.PairsFor(hostID, cv.Capability.LocalID)
		for _, p := range pairs {
			if p.OfferingID != offeringID {
				continue
			}
			matchedCount++
			if body.LocalID == "" || body.LocalID == cv.Capability.LocalID {
				target = cv.Capability
			}
		}
	}
	if target == nil {
		adminError(w, http.StatusConflict, "runner_not_matched", "the capability is not matched to the offer")
		return
	}
	if body.LocalID == "" && matchedCount > 1 {
		adminError(w, http.StatusBadRequest, "invalid_request", "several capabilities match this offer; pass local_id")
		return
	}
	key := offers.PairKey{HostID: hostID, LocalID: target.LocalID, OfferingID: offeringID}
	runID := s.certEngine.Start(key, "operator", v.Operator, target)
	adminJSON(w, http.StatusAccepted, map[string]any{"run_id": runID, "state": certification.RunRunning})
}
