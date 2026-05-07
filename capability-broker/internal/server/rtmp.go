package server

import "net/http"

// rtmpCloseSession handles the customer-driven termination trigger
// per docs/exec-plans/active/0011-followup §7.4. The URL path's
// {session_id} is itself the bearer secret (12 random bytes hex,
// minted at session-open); 404 covers both unknown and expired
// sessions without leaking which.
//
// Tear-down is delegated to Store.Close: it cancels the encoder
// goroutine, closes the FLV pipe, and removes the scratch directory.
// The payment-daemon settlement (final Reconcile + CloseSession) is
// driven by the interim-debit machinery on its next tick.
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
	if !s.rtmpStore.Close(sessionID, "customer_close_session") {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
