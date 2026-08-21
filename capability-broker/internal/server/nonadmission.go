package server

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/settlement"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// nonAdmissionQuery is what a consumer supplies to have a claim bound to
// its own job.
//
// The broker can only verify the request id — the rest describes an
// exchange that never happened, so there is nothing of its own to check
// it against. They are signed anyway, as scope: a record that named only
// a request id could be replayed against a different envelope that
// happened to carry the same id from another payer. The consumer
// compares every field to its own record before acting, which is what
// makes echoed context sufficient.
type nonAdmissionQuery struct {
	Protocol     string `json:"protocol"`
	WorkID       string `json:"work_id"`
	SenderHex    string `json:"sender"`
	RecipientHex string `json:"recipient"`
	QuoteID      string `json:"quote_id"`
	QuoteVersion uint64 `json:"quote_version"`
	JobIssuedAt  string `json:"job_issued_at"`
	CapabilityID string `json:"capability,omitempty"`
	OfferingID   string `json:"offering,omitempty"`
}

// handleNonAdmission answers "did you ever admit an exchange for this
// request id" with a signed record.
//
// Keyed on the request id the CONSUMER issued, so the answer is
// retrievable without anything the customer holds — which is the point.
// A customer that took the work and hid the receipt cannot also suppress
// this.
func (s *Server) handleNonAdmission(w http.ResponseWriter, r *http.Request) {
	requestID := r.PathValue("request_id")
	if strings.TrimSpace(requestID) == "" {
		livepeerheader.WriteBadRequest(w, "request_id is required")
		return
	}
	if s.settlementSigner == nil {
		// An unsigned non-admission record is an anonymous assertion
		// that somebody should be refunded. Refuse rather than emit one.
		livepeerheader.WriteError(w, http.StatusNotImplemented, livepeerheader.ErrInternalError,
			"this broker holds no delegated settlement key, so it cannot produce attributable "+
				"non-admission evidence")
		return
	}
	if s.sessionStore == nil {
		livepeerheader.WriteError(w, http.StatusNotImplemented, livepeerheader.ErrInternalError,
			"no durable store; this broker cannot attest to what it did or did not admit")
		return
	}

	var q nonAdmissionQuery
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil || (len(raw) > 0 && json.Unmarshal(raw, &q) != nil) {
		livepeerheader.WriteBadRequest(w, "body must be a JSON object")
		return
	}

	// Any record at all refutes the claim. Attesting while an exchange
	// is in flight would sign a statement the next second contradicts.
	exists, state, err := s.sessionStore.HasJobRecord(requestID)
	if err != nil {
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"reading the job record failed")
		return
	}
	if exists {
		livepeerheader.WriteError(w, http.StatusConflict, livepeerheader.ErrAdmitted,
			"this broker has a record for that request id (state "+state+"); it was admitted, "+
				"and its settlement is at /v1/settlement/{id}")
		return
	}

	coverage, err := s.sessionStore.CoverageStartedAt()
	if err != nil {
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"reading record coverage failed")
		return
	}
	// Absence across a gap in this broker's own records is not evidence
	// of anything. Refuse rather than make a consumer catch it — a claim
	// the consumer must reject is noise, and emitting it invites somebody
	// to act on it who forgot to check.
	if issued := parseTime(q.JobIssuedAt); !issued.IsZero() && issued.Before(coverage) {
		livepeerheader.WriteError(w, http.StatusConflict, livepeerheader.ErrCoverageGap,
			"this broker's records begin at "+coverage.Format(time.RFC3339Nano)+
				", after the job was issued; absence before that is forgetting, not non-admission")
		return
	}

	rec := &pb.NonAdmissionRecord{
		Protocol:          q.Protocol,
		RequestId:         requestID,
		WorkId:            q.WorkID,
		Sender:            decodeHex(q.SenderHex),
		Recipient:         decodeHex(q.RecipientHex),
		BrokerEthAddress:  s.cfg.Identity.OrchEthAddress,
		ObservedAt:        time.Now().UTC().Format(time.RFC3339Nano),
		CoverageStartedAt: coverage.Format(time.RFC3339Nano),
		Outcome:           pb.NonAdmissionRecord_NOT_ADMITTED,
	}
	if q.QuoteID != "" {
		rec.AcceptedQuoteRef = &pb.QuoteRef{QuoteId: q.QuoteID, QuoteVersion: q.QuoteVersion}
	}

	encoded, err := settlement.EncodeNonAdmission(rec, s.settlementSigner)
	if err != nil {
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"signing the non-admission record failed: "+err.Error())
		return
	}
	w.Header().Set(livepeerheader.NonAdmission, encoded)
	writeJSON(w, http.StatusOK, map[string]any{
		"request_id":          requestID,
		"outcome":             "NOT_ADMITTED",
		"observed_at":         rec.GetObservedAt(),
		"coverage_started_at": rec.GetCoverageStartedAt(),
		"non_admission":       encoded,
	})
}

func decodeHex(s string) []byte {
	b, err := hex.DecodeString(strings.TrimPrefix(strings.TrimPrefix(s, "0x"), "0X"))
	if err != nil {
		return nil
	}
	return b
}

func parseTime(s string) time.Time {
	if strings.TrimSpace(s) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}
