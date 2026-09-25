package sessionengine

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type revisionFailure struct{ reason, stage, code string }

func (e *revisionFailure) Error() string { return e.stage + ": " + e.reason }
func receiverRevisionFailure(stage string, err error) error {
	return &revisionFailure{reason: payment.SafeAdmissionReason(err), stage: stage, code: status.Code(err).String()}
}

func (e *Engine) revisionDecision(rec *sessionstore.Record, reason, stage, code string) *sessionstore.RevisionDecision {
	i := rec.RevisionIntent
	var auth pb.SpendAuthorization
	_ = proto.Unmarshal(i.AuthorizationBytes, &auth)
	p := auth.GetPayload()
	d := &sessionstore.RevisionDecision{Outcome: "pending", Reason: reason, Stage: stage, ReceiverCode: code,
		SessionID: rec.SessionID, GatewaySessionID: rec.GatewaySessionID, RequestID: i.RequestID,
		AuthorizationID: p.GetAuthorizationId(), PredecessorID: p.GetPredecessorAuthorizationId(), Revision: p.GetRevision(), ObservedAt: e.cfg.Now().UTC()}
	d.RequestedReservationWei, d.ReservationWei = i.ReservationWei, i.ReservationWei
	d.MaxUnits, d.MaxDebitWei = p.GetMaxTotalUnits(), new(big.Int).SetBytes(p.GetMaxDebitWei().GetValue()).String()
	if prior := i.Decision; prior != nil {
		d.RequestedReservationWei, d.ReservationWei, d.BilledWei, d.ActualUnits = prior.RequestedReservationWei, prior.ReservationWei, prior.BilledWei, prior.ActualUnits
	}
	return d
}

func (e *Engine) saveRevisionDecision(rec *sessionstore.Record, d *sessionstore.RevisionDecision, cancel bool) error {
	err := e.cfg.Store.Update(rec.SessionID, func(r *sessionstore.Record) error {
		if r.RevisionIntent == nil {
			return fmt.Errorf("revision intent missing")
		}
		r.RevisionIntent.Decision, r.LastRevision = d, d
		r.RevisionIntent.CancelRequested = cancel
		return nil
	})
	if err == nil {
		rec.RevisionIntent.Decision, rec.RevisionIntent.CancelRequested = d, cancel
		rec.LastRevision = d
	}
	return err
}

func (e *Engine) observeRevision(d *sessionstore.RevisionDecision) {
	if d == nil {
		return
	}
	e.cfg.Log.Info("session revision decision", "session_id", d.SessionID, "gateway_session_id", d.GatewaySessionID,
		"request_id", d.RequestID, "authorization_id", d.AuthorizationID, "predecessor_authorization_id", d.PredecessorID,
		"revision", d.Revision, "outcome", d.Outcome, "reason", d.Reason, "stage", d.Stage, "receiver_code", d.ReceiverCode, "requested_reservation_wei", d.RequestedReservationWei, "reservation_wei", d.ReservationWei, "receiver_billed_wei", d.BilledWei, "receiver_actual_units", d.ActualUnits, "max_total_units", d.MaxUnits, "max_debit_wei", d.MaxDebitWei)
	if e.cfg.OnRevision != nil {
		e.cfg.OnRevision(d.Outcome, d.Stage, d.Reason)
	}
}

func (e *Engine) pendingRevisionError(id string) error {
	r, err := e.cfg.Store.Get(id)
	if err != nil || r.RevisionIntent == nil {
		return &RetryableError{Err: errors.New("revision recovery pending")}
	}
	i := r.RevisionIntent
	return &RetryableError{Err: errors.New("revision recovery pending"), Revision: i.Decision}
}

// prepareRevisionEvidence runs only after receiver acceptance or non-admission
// proof. It remains on the sealed intent until authority/result commit atomically.
func (e *Engine) prepareRevisionEvidence(rec *sessionstore.Record, admitted bool, basis string) error {
	i := rec.RevisionIntent
	var auth pb.SpendAuthorization
	if err := proto.Unmarshal(i.AuthorizationBytes, &auth); err != nil {
		return err
	}
	p := auth.GetPayload()
	if p == nil {
		return fmt.Errorf("revision payload missing")
	}
	d := i.Decision
	if d == nil {
		d = e.revisionDecision(rec, "PREVIOUS_REASON_UNAVAILABLE", "reconciliation", "")
	} else {
		clone := *d
		d = &clone
	}
	d.Outcome, d.NextRetryAt = "refused", time.Time{}
	outcome := pb.SessionRevisionRecord_NOT_ADMITTED
	if admitted {
		if d.Reason == "ADMISSION_PENDING" {
			d.Reason, d.Stage, d.ReceiverCode = "ADMITTED", "admission", "OK"
		}
		d.Outcome = "admitted"
		outcome = pb.SessionRevisionRecord_ADMITTED
	}
	if !admitted && basis != "receiver_fenced" && basis != "expired_unused" {
		return fmt.Errorf("revision non-admission proof missing")
	}
	d.ObservedAt = e.cfg.Now().UTC()
	fingerprint := sha256.Sum256(i.AuthorizationBytes)
	evidence, err := proto.Marshal(&pb.SessionRevisionRecord{WholesaleAccountId: rec.WholesaleAccountID,
		EvidenceDomain: "livepeer-session-revision/v1", Protocol: "paid-session/v1", SettlementDomainId: rec.SettlementDomainID,
		BrokerUri: p.GetBrokerUri(), SessionId: rec.SessionID, GatewaySessionId: rec.GatewaySessionID, RequestId: i.RequestID,
		Payer: p.GetPayer(), Payee: p.GetPayee(), AuthorizationId: p.GetAuthorizationId(), PredecessorAuthorizationId: p.GetPredecessorAuthorizationId(),
		Revision: p.GetRevision(), AuthorizationFingerprint: fingerprint[:], AcceptedQuoteRef: &pb.QuoteRef{QuoteId: rec.QuoteID, QuoteVersion: rec.QuoteVersion, ConstraintFingerprint: rec.ConstraintFingerprint, RouteFingerprint: rec.RouteFingerprint},
		Outcome: outcome, NonAdmissionBasis: basis, Reason: d.Reason, Stage: d.Stage, ReceiverCode: d.ReceiverCode, ObservedAt: d.ObservedAt.Format(time.RFC3339Nano), MaxTotalUnits: p.GetMaxTotalUnits(), MaxDebitWei: p.GetMaxDebitWei(),
	})
	if err != nil {
		return err
	}
	err = e.cfg.Store.Update(rec.SessionID, func(r *sessionstore.Record) error {
		if r.RevisionIntent == nil {
			return fmt.Errorf("revision intent missing")
		}
		r.RevisionIntent.Decision, r.RevisionIntent.Evidence = d, evidence
		return nil
	})
	if err == nil {
		i.Decision, i.Evidence = d, evidence
	}
	return err
}
