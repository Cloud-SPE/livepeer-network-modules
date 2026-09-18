package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/certification"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
)

// The usage callback for a session under certification.
//
// A session runner reports usage asynchronously, to the callback it was
// handed at create (paid-session/v1 §7.1). Certification gives it one
// of these instead of a paid session's, so that proving a runner can be
// billed does not require minting a session the payment machinery would
// then have to account for. The runner cannot tell the difference: the
// URL is opaque and the envelope is the same one it always sends.
//
// Scope is deliberately narrow. The tap accepts usage for exactly one
// certification session, exists only while that session is open, and
// debits nothing.

// maxCertEventBytes bounds one callback body. A usage envelope is a
// handful of fields; anything larger is a runner sending something else.
const maxCertEventBytes = 64 << 10

func (s *Server) handleCertificationUsage(w http.ResponseWriter, r *http.Request) {
	if s.certEngine == nil {
		writeUniformUnauthorized(w)
		return
	}
	tapID := strings.TrimSpace(r.PathValue("tap_id"))
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if tapID == "" || token == "" {
		// Same answer as a wrong token: a prober learns nothing about
		// which certification runs are open.
		writeUniformUnauthorized(w)
		return
	}
	var body sessionEventBody
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxCertEventBytes))
	if err != nil || json.Unmarshal(raw, &body) != nil {
		livepeerheader.WriteBadRequest(w, "body must be a JSON event envelope")
		return
	}
	ev := certification.UsageEvent{
		Sequence: body.Sequence, EventType: body.EventType, At: time.Now().UTC(),
	}
	if body.Usage != nil {
		ev.Unit = body.Usage.Unit
		ev.Total = body.Usage.Total
	}
	if !s.certEngine.RecordUsageEvent(tapID, token, ev) {
		writeUniformUnauthorized(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true})
}
