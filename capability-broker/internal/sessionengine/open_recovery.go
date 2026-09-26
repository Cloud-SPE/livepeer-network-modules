package sessionengine

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

func (e *Engine) openRecoveryRecord(req OpenRequest, id string, p *pb.SpendAuthorizationPayload, fp []byte) *sessionstore.Record {
	q := req.AcceptedQuoteRef
	if q == nil {
		q = p.GetAcceptedPrice().GetQuoteRef()
	}
	return &sessionstore.Record{
		WholesaleAccountID: p.GetWholesaleAccountId(), SessionID: id, GatewaySessionID: req.GatewaySessionID, WorkID: p.GetAuthorizationId(), Capability: req.Spec.Capability, Offering: req.Spec.Offering, BackendRef: req.Spec.BackendRef,
		QuoteID: q.GetQuoteId(), QuoteVersion: q.GetQuoteVersion(), ConstraintFingerprint: append([]byte(nil), q.GetConstraintFingerprint()...), RouteFingerprint: append([]byte(nil), q.GetRouteFingerprint()...),
		Sender: append([]byte(nil), p.GetPayer()...), OpenFingerprint: fp, SettlementDomainID: p.GetSettlementDomainId(), AccountAuthorizationID: p.GetAuthorizationId(),
		AuthorizationMaxUnits: p.GetMaxTotalUnits(), AuthorizationMaxDebitWei: new(big.Int).SetBytes(p.GetMaxDebitWei().GetValue()).String(), AuthorizationReservedWei: req.InitialReservationWei.String(),
		AcceptedPriceWei: new(big.Int).SetBytes(p.GetAcceptedPrice().GetPricePerUnitWei().GetValue()).String(), AcceptedPricePerUnits: p.GetAcceptedPrice().GetUnitsPerPrice(),
		Unit: req.Spec.WorkUnit, SettlementSeq: 1, State: sessionstore.StateWindingDown, CloseReason: ReasonOpenFailed, CreatedAt: e.cfg.Now(), LastEventAt: e.cfg.Now(),
	}
}

func (e *Engine) reconcileFailedOpen(ctx context.Context, id string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := e.cleanupOpenReservation(cleanupCtx, id); err != nil {
		e.cfg.Log.Warn("initial admission recovery pending", "request_id", id, "err", err)
		return &RetryableError{Err: fmt.Errorf("initial admission outcome pending reconciliation")}
	}
	if r, err := e.cfg.Store.Reservation(id); err == nil && r.AdmissionCanceled {
		return protoErr("payment_invalid", "authorization was not admitted and is now canceled")
	}
	if sid, err := e.cfg.Store.SessionIDForRequest(id); err == nil {
		if rec, err := e.cfg.Store.Get(sid); err == nil && rec.Terminal() {
			return protoErr("session_terminal", "initial admission was recovered and closed; query exchange by request id")
		}
	}
	return &RetryableError{Err: fmt.Errorf("initial admission accounting pending; query exchange by request id")}
}

// Fence and inspect are one receiver transaction. A not-found lookup alone
// cannot exclude a delayed admission arriving after recovery releases capacity.
func (e *Engine) resolveOpenAdmission(ctx context.Context, r sessionstore.OpenReservation) error {
	recovery, ok := e.cfg.Payment.(payment.AdmissionRecovery)
	if !ok {
		return fmt.Errorf("receiver admission recovery unavailable")
	}
	outcome, err := recovery.CancelAuthorizationAdmission(ctx, r.AdmissionIntent.AuthorizationBytes)
	if err != nil {
		return err
	}
	if outcome == nil {
		return fmt.Errorf("receiver admission outcome missing")
	}
	if outcome.Canceled || (outcome.Authorization != nil && outcome.Authorization.State == int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_EXPIRED_UNUSED)) {
		return e.cfg.Store.UpdateReservation(r.RequestID, func(current *sessionstore.OpenReservation) error { current.AdmissionCanceled = true; return nil })
	}
	usage := outcome.Authorization
	if usage == nil || (usage.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) && usage.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED)) {
		return fmt.Errorf("receiver initial admission remains unresolved")
	}
	rec := r.AdmissionIntent.Record
	rec.RunnerSessionID = r.RunnerSessionID
	rec.RunnerTerminated = true
	rec.CapacityRef = ""
	if err := reconcileReceiverUsage(&rec, usage); err != nil {
		return err
	}
	if err := e.cfg.Store.CreateIndexed(&rec, r.RequestID); err != nil {
		return err
	}
	mu := e.sessionMu(rec.SessionID)
	mu.Lock()
	defer mu.Unlock()
	e.winddownLocked(ctx, rec.SessionID, ReasonOpenFailed)
	return nil
}
