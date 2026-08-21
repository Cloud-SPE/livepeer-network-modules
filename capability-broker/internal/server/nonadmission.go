package server

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
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
	Protocol              string `json:"protocol"`
	WorkID                string `json:"work_id"`
	SenderHex             string `json:"sender"`
	RecipientHex          string `json:"recipient"`
	QuoteID               string `json:"quote_id"`
	QuoteVersion          uint64 `json:"quote_version"`
	ConstraintFingerprint string `json:"constraint_fingerprint"`
	RouteFingerprint      string `json:"route_fingerprint"`
	JobIssuedAt           string `json:"job_issued_at"`
}

// validate requires every field and parses it strictly.
//
// Fail-closed because the alternative fails OPEN in the direction that
// matters: a missing job_issued_at parsed as the zero time skipped the
// coverage check entirely, so the one query a broker with a gap in its
// records must refuse was the one that omitted a field. Empty scope was
// signable too, producing a record that binds to nothing and can be
// replayed against any envelope carrying the same request id.
func (q *nonAdmissionQuery) validate() (time.Time, error) {
	switch q.Protocol {
	case "paid-job/v1", "paid-session/v1":
	default:
		return time.Time{}, fmt.Errorf("protocol must be paid-job/v1 or paid-session/v1, got %q", q.Protocol)
	}
	if strings.TrimSpace(q.WorkID) == "" {
		return time.Time{}, errors.New("work_id is required")
	}
	sender, err := strictHex(q.SenderHex)
	if err != nil {
		return time.Time{}, fmt.Errorf("sender: %w", err)
	}
	if len(sender) != 20 {
		return time.Time{}, fmt.Errorf("sender must be 20 bytes, got %d", len(sender))
	}
	recipient, err := strictHex(q.RecipientHex)
	if err != nil {
		return time.Time{}, fmt.Errorf("recipient: %w", err)
	}
	if len(recipient) != 20 {
		return time.Time{}, fmt.Errorf("recipient must be 20 bytes, got %d", len(recipient))
	}
	if strings.TrimSpace(q.QuoteID) == "" {
		return time.Time{}, errors.New("quote_id is required")
	}
	if q.QuoteVersion == 0 {
		return time.Time{}, errors.New("quote_version is required and must be >= 1")
	}
	if _, err := strictHex(q.ConstraintFingerprint); err != nil {
		return time.Time{}, fmt.Errorf("constraint_fingerprint: %w", err)
	}
	if _, err := strictHex(q.RouteFingerprint); err != nil {
		return time.Time{}, fmt.Errorf("route_fingerprint: %w", err)
	}
	if strings.TrimSpace(q.JobIssuedAt) == "" {
		return time.Time{}, errors.New("job_issued_at is required: without it the broker cannot " +
			"tell whether its own records cover the period you are asking about")
	}
	issued, err := time.Parse(time.RFC3339Nano, q.JobIssuedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("job_issued_at must be RFC3339: %w", err)
	}
	return issued.UTC(), nil
}

// strictHex decodes required hex, rejecting empty and malformed input
// rather than silently yielding nil.
func strictHex(s string) ([]byte, error) {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "0x"), "0X")
	if trimmed == "" {
		return nil, errors.New("required")
	}
	b, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("not hex: %w", err)
	}
	return b, nil
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
	if err != nil || json.Unmarshal(raw, &q) != nil {
		livepeerheader.WriteBadRequest(w, "body must be a JSON object")
		return
	}
	issuedAt, err := q.validate()
	if err != nil {
		livepeerheader.WriteBadRequest(w, "non-admission query: "+err.Error())
		return
	}

	// A record already issued is returned verbatim. Re-signing would
	// produce a second signed statement about one fact, under a later
	// observed_at, and a consumer holding both cannot tell that they
	// agree rather than conflict.
	if prior, found, ferr := s.sessionStore.NonAdmissionFor(requestID); ferr == nil && found {
		w.Header().Set(livepeerheader.NonAdmission, prior)
		writeJSON(w, http.StatusOK, map[string]any{
			"request_id":    requestID,
			"outcome":       "NOT_ADMITTED",
			"replayed":      true,
			"non_admission": prior,
		})
		return
	}

	coverage, err := s.sessionStore.CoverageStartedAt()
	if err != nil {
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"reading record coverage failed")
		return
	}
	// Absence across a gap in this broker's own records is not evidence.
	// Refused rather than left for the consumer to catch: a claim they
	// must reject is noise, and emitting it invites somebody who forgot
	// to check.
	if issuedAt.Before(coverage) {
		livepeerheader.WriteError(w, http.StatusConflict, livepeerheader.ErrCoverageGap,
			"this broker's records begin at "+coverage.Format(time.RFC3339Nano)+
				", after the job was issued; absence before that is forgetting, not non-admission")
		return
	}

	sender, _ := strictHex(q.SenderHex)
	recipient, _ := strictHex(q.RecipientHex)
	cfp, _ := strictHex(q.ConstraintFingerprint)
	rfp, _ := strictHex(q.RouteFingerprint)

	rec := &pb.NonAdmissionRecord{
		Protocol:  q.Protocol,
		RequestId: requestID,
		WorkId:    q.WorkID,
		Sender:    sender,
		Recipient: recipient,
		AcceptedQuoteRef: &pb.QuoteRef{
			QuoteId:               q.QuoteID,
			QuoteVersion:          q.QuoteVersion,
			ConstraintFingerprint: cfp,
			RouteFingerprint:      rfp,
		},
		BrokerEthAddress:  s.cfg.Identity.OrchEthAddress,
		ObservedAt:        time.Now().UTC().Format(time.RFC3339Nano),
		CoverageStartedAt: coverage.Format(time.RFC3339Nano),
		Outcome:           pb.NonAdmissionRecord_NOT_ADMITTED,
	}

	encoded, err := settlement.EncodeNonAdmission(rec, s.settlementSigner)
	if err != nil {
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"signing the non-admission record failed: "+err.Error())
		return
	}

	// Persist and re-check absence in ONE transaction. Checking then
	// signing leaves a window where an exchange is admitted in between,
	// and the broker emits a signed statement that it never admitted
	// something it is at that moment running.
	existing, err := s.sessionStore.RecordNonAdmission(requestID, encoded, time.Now().UTC())
	if errors.Is(err, sessionstore.ErrExists) {
		livepeerheader.WriteError(w, http.StatusConflict, livepeerheader.ErrAdmitted,
			"this broker has a record for that request id; it was admitted, and its "+
				"settlement is at /v1/settlement/{id}")
		return
	}
	if err != nil {
		livepeerheader.WriteError(w, http.StatusInternalServerError, livepeerheader.ErrInternalError,
			"persisting the non-admission record failed")
		return
	}
	if existing != "" {
		encoded = existing // another request won the race; one fact, one record
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
