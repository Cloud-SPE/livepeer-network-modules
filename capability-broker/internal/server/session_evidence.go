package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionengine"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/settlement"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

// The same unguessable session identifier and signed-evidence access model as
// settlement lookup permits LOC to read directly without a gateway credential.
func (s *Server) handleSessionRevision(w http.ResponseWriter, r *http.Request) {
	id, requestID := r.PathValue("id"), r.PathValue("request_id")
	if s.sessionStore == nil {
		http.NotFound(w, r)
		return
	}
	// Also accept the consumer-owned gateway_session_id while its session
	// index is retained, as settlement lookup does.
	if _, err := s.sessionStore.Get(id); errors.Is(err, sessionstore.ErrNotFound) {
		if rec, aliasErr := s.sessionStore.GetByGatewaySessionID(id); aliasErr == nil {
			id = rec.SessionID
		}
	}
	result, err := s.sessionStore.GetRevision(id, requestID)
	if errors.Is(err, sessionstore.ErrNotFound) {
		rec, readErr := s.sessionStore.Get(id)
		if readErr == nil && rec.RevisionIntent != nil && rec.RevisionIntent.RequestID == requestID {
			w.Header().Set("Retry-After", "1")
			writeJSON(w, http.StatusAccepted, map[string]any{"outcome": "pending", "revision": rec.LastRevision})
			return
		}
		http.NotFound(w, r)
		return
	}
	if err != nil {
		livepeerheader.WriteError(w, http.StatusServiceUnavailable, "revision_pending", "revision lookup unavailable")
		return
	}
	if len(result.RevisionEvidence) == 0 {
		livepeerheader.WriteError(w, http.StatusConflict, "revision_evidence_unavailable", "historical revision has no retained receiver proof")
		return
	}
	envelope := result.RevisionEnvelope
	if envelope == "" {
		var record pb.SessionRevisionRecord
		if err := proto.Unmarshal(result.RevisionEvidence, &record); err != nil {
			livepeerheader.WriteError(w, http.StatusServiceUnavailable, "revision_evidence_unavailable", "revision evidence unreadable")
			return
		}
		envelope, err = settlement.EncodeRevision(&record, s.settlementSigner)
		if err == nil {
			envelope, err = s.sessionStore.RecordRevisionEnvelope(id, requestID, result.RevisionEvidence, envelope)
		}
		if err != nil {
			livepeerheader.WriteError(w, http.StatusServiceUnavailable, "revision_evidence_unavailable", "signed revision evidence unavailable")
			return
		}
	}
	w.Header().Set("Livepeer-Revision-Evidence", envelope)
	writeJSON(w, http.StatusOK, map[string]any{"revision": result.Decision, "revision_evidence": envelope})
}

func revisionFromError(err error) *sessionstore.RevisionDecision {
	var pe *sessionengine.ProtocolError
	if errors.As(err, &pe) {
		return pe.Revision
	}
	var re *sessionengine.RetryableError
	if errors.As(err, &re) {
		return re.Revision
	}
	return nil
}

func (s *Server) signedSessionSettlement(ctx context.Context, id string) (*pb.SettlementRecord, string, error) {
	record, err := s.sessionEngine.RecordSettlement(ctx, id)
	if err != nil || record == nil {
		return record, "", err
	}
	r, err := s.sessionStore.Get(id)
	if err != nil {
		return nil, "", err
	}
	if r.Terminal() && len(r.TerminalSettlement) > 0 {
		record = &pb.SettlementRecord{}
		if err := proto.Unmarshal(r.TerminalSettlement, record); err != nil {
			return nil, "", err
		}
	}
	if r.Terminal() && r.TerminalSettlementEnvelope != "" {
		return record, r.TerminalSettlementEnvelope, nil
	}
	envelope, err := settlement.Encode(record, s.settlementSigner)
	if err == nil && r.Terminal() && s.settlementSigner != nil {
		// Compare with the frozen bytes read from the store. No current config
		// or re-marshalling can change the terminal fact being signed.
		envelope, err = s.sessionStore.RecordTerminalSettlementEnvelope(id, r.TerminalSettlement, envelope)
	}
	return record, envelope, err
}
