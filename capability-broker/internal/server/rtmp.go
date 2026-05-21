package server

import (
	"log"
	"net/http"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
)

// rtmpCloseSession handles the customer-driven termination trigger
// per docs/exec-plans/active/0011-followup §7.4. The URL path's
// {session_id} is itself the bearer secret (12 random bytes hex,
// minted at session-open); 404 covers both unknown and expired
// sessions without leaking which.
//
// Tear-down is delegated to Store.Close: it cancels the encoder
// goroutine, closes the FLV pipe, and removes the scratch directory.
// The payment-daemon settlement (final Reconcile + CloseSession) is
// driven by the interim-debit machinery on its next tick. The
// X-Livepeer-Settlement response header on this 204 carries the
// broker-authoritative SettlementRecord (plan 0034 §7.3) so the
// gateway can persist final accounting without polling.
func (s *Server) rtmpCloseSession(w http.ResponseWriter, r *http.Request) {
	if s.rtmpStore == nil {
		http.NotFound(w, r)
		return
	}
	sessionID := r.PathValue("session_id")
	if sessionID == "" {
		http.NotFound(w, r)
		return
	}
	rec := s.rtmpStore.Get(sessionID)
	if rec == nil {
		http.NotFound(w, r)
		return
	}

	// Capture settlement before Close() removes the record. A nil
	// SettlementInputs (legacy/stub payment) means we can't build a
	// record, which is fine — the close still tears the session down.
	var settlementHeader string
	if rec.SettlementInputs != nil {
		var actual uint64
		if rec.LiveCounter != nil {
			actual = rec.LiveCounter.CurrentUnits()
		}
		if record := middleware.BuildSettlementRecord(*rec.SettlementInputs, actual, ""); record != nil {
			if encoded, err := middleware.EncodeSettlementRecord(record); err == nil {
				settlementHeader = encoded
			} else {
				log.Printf("warning: rtmp settlement encode failed session_id=%s: %v", sessionID, err)
			}
		}
	}

	if !s.rtmpStore.Close(sessionID, "customer_close_session") {
		http.NotFound(w, r)
		return
	}
	if settlementHeader != "" {
		w.Header().Set(livepeerheader.Settlement, settlementHeader)
	}
	w.WriteHeader(http.StatusNoContent)
}
