package server

import (
	"net/http"
	"strconv"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
)

// handleExchangeByRequestID answers "what happened to this request",
// keyed on the id the CONSUMER issued.
//
// It exists to prevent conservative overcharging. A clearinghouse whose
// customer withheld the settlement also lacks the broker job id, and
// every other lookup on this broker is keyed on something the customer
// holds — so a broker could be sitting on a valid signed settlement
// while the clearinghouse, unable to find it, charged the customer in
// full on the assumption that nothing was recoverable.
//
// It is deliberately NOT refund authority and answers no question about
// whether an envelope can still be spent. It reports what this broker
// did, which is the input to charging correctly rather than
// conservatively.
func (s *Server) handleExchangeByRequestID(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("request_id")
	if requestID == "" {
		livepeerheader.WriteBadRequest(w, "request_id is required")
		return
	}
	if s.jobIdem == nil {
		livepeerheader.WriteError(w, http.StatusNotImplemented, livepeerheader.ErrInternalError,
			"this broker keeps no durable exchange records")
		return
	}

	rec, err := s.jobIdem.ByRequestID(requestID)
	if err == nil && rec != nil {
		s.writeExchangeState(w, rec)
		return
	}

	// No exchange. A non-admission record may already have been issued
	// and, if so, is returned here too — one lookup, every outcome, so a
	// consumer does not have to know which surface to try.
	if s.sessionStore != nil {
		if envelope, found, ferr := s.sessionStore.NonAdmissionFor(requestID); ferr == nil && found {
			w.Header().Set(livepeerheader.NonAdmission, envelope)
			writeJSON(w, http.StatusOK, map[string]any{
				"request_id":    requestID,
				"outcome":       "NOT_ADMITTED",
				"non_admission": envelope,
			})
			return
		}
	}

	// Admitted, record aged out. Distinct from both a settlement and a
	// non-admission: the exchange happened, and this broker can no
	// longer say what it cost.
	if s.sessionStore != nil {
		if admitted, jobID, aerr := s.sessionStore.WasAdmitted(requestID); aerr == nil && admitted {
			writeJSON(w, http.StatusOK, map[string]any{
				"request_id": requestID,
				"job_id":     jobID,
				"outcome":    "ADMITTED_EVIDENCE_EXPIRED",
				"detail":     "admitted; the detailed record has aged out of retention",
			})
			return
		}
	}

	// Nothing at all. Distinct from NOT_ADMITTED: this broker has not
	// been asked to attest, and silence is not a claim.
	writeJSON(w, http.StatusNotFound, map[string]any{
		"request_id": requestID,
		"outcome":    "NO_RECORD",
		"detail": "this broker has no record for that request id and has issued no " +
			"non-admission claim; ask POST /v1/non-admission/{request_id} if you need one",
	})
}

// writeExchangeState reports a record's state and, when terminal, the
// settlement itself.
func (s *Server) writeExchangeState(w http.ResponseWriter, rec *sessionstore.JobRecord) {
	body := map[string]any{
		"request_id": rec.RequestID,
		// The stable polling identity. A consumer that has to come back
		// needs something to come back WITH, and its own request id is
		// the only handle it is guaranteed to still have.
		"job_id": rec.JobID,
		"state":  rec.State,
	}
	switch rec.State {
	case sessionstore.JobTerminal, sessionstore.JobAbandoned:
		if !rec.EndedAt.IsZero() {
			body["ended_at"] = rec.EndedAt.Format(time.RFC3339Nano)
		}
		w.Header().Set(livepeerheader.JobID, rec.JobID)

		// SETTLED requires an actual signed settlement, not merely a
		// terminal state.
		//
		// A crash leftover closed out at its deadline is terminal and
		// has no settlement; reporting it as SETTLED with a zero status
		// tells a consumer the exchange cost nothing, which is a claim
		// about money that nothing supports. The outcome is derived from
		// the EVIDENCE rather than from the state, so a record that
		// somehow lacks one can never be reported as settled.
		if rec.Settlement == "" {
			body["outcome"] = "ADMITTED_OUTCOME_UNKNOWN"
			body["detail"] = "this broker admitted the exchange and holds no signed settlement " +
				"for it; it produced no recorded outcome"
			writeJSON(w, http.StatusOK, body)
			return
		}
		body["outcome"] = "SETTLED"
		body["status"] = rec.Status
		body["work_units"] = rec.WorkUnits
		body["unit"] = rec.Unit
		body["settlement"] = rec.Settlement
		w.Header().Set(livepeerheader.Settlement, rec.Settlement)
		w.Header().Set(livepeerheader.WorkUnits, strconv.FormatUint(rec.WorkUnits, 10))
		writeJSON(w, http.StatusOK, body)
	case sessionstore.JobAccountingPending:
		// Delivered, debit outstanding. 202 rather than 200 because it
		// is not terminal: this exchange WILL settle, and charging
		// conservatively now would charge for an outcome still in
		// motion.
		body["outcome"] = "ACCOUNTING_PENDING"
		body["work_units"] = rec.WorkUnits
		body["unit"] = rec.Unit
		if rec.Pending != nil {
			body["debit_attempts"] = rec.Pending.Attempts
		}
		w.Header().Set(livepeerheader.Error, livepeerheader.ErrAccountingPending)
		writeJSON(w, http.StatusAccepted, body)
	default:
		body["outcome"] = "IN_FLIGHT"
		if !rec.Deadline.IsZero() {
			body["deadline"] = rec.Deadline.Format(time.RFC3339Nano)
		}
		writeJSON(w, http.StatusAccepted, body)
	}
}
