package sessionengine

import (
	"context"
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

// resumeRevisionLocked runs with the session mutex held. Explicit caller retries
// may bypass the background cooldown; all paths retain the same durable identity.
func (e *Engine) resumeRevisionLocked(ctx context.Context, id string, force ...bool) (*TopUpResult, error) {
	rec, err := e.cfg.Store.Get(id)
	if err != nil {
		return nil, err
	}
	if rec.RevisionIntent == nil {
		return nil, fmt.Errorf("session revision intent missing")
	}
	if (len(force) == 0 || !force[0]) && e.cfg.Now().Before(rec.RevisionIntent.NextRetryAt) {
		return nil, e.pendingRevisionError(id)
	}
	result, err := e.resolveRevisionLocked(ctx, rec)
	if err == nil {
		return result, nil
	}
	var rejected *ProtocolError
	if errors.As(err, &rejected) {
		return nil, err
	}
	if saveErr := e.cfg.Store.Update(id, func(r *sessionstore.Record) error {
		if r.RevisionIntent == nil {
			return nil
		}
		intent := r.RevisionIntent
		if !intent.CancelRequested {
			reason, stage, code := "RECONCILIATION_PENDING", "reconciliation", ""
			var failure *revisionFailure
			if errors.As(err, &failure) {
				reason, stage, code = failure.reason, failure.stage, failure.code
			}
			intent.Decision = e.revisionDecision(r, reason, stage, code)
		}
		if intent.Decision == nil {
			intent.Decision = e.revisionDecision(r, "PREVIOUS_REASON_UNAVAILABLE", "reconciliation", "")
		}
		intent.Decision.Outcome = "pending"
		if intent.Failures < 7 {
			intent.Failures++
		}
		delay := time.Second << (intent.Failures - 1)
		if delay > time.Minute {
			delay = time.Minute
		}
		intent.NextRetryAt = e.cfg.Now().Add(delay)
		intent.Decision.NextRetryAt = intent.NextRetryAt
		r.LastRevision = intent.Decision
		return nil
	}); saveErr != nil {
		return nil, &RetryableError{Err: saveErr}
	}
	if current, readErr := e.cfg.Store.Get(id); readErr == nil {
		e.observeRevision(current.LastRevision)
	}
	return nil, e.pendingRevisionError(id)
}

func (e *Engine) resolveRevisionLocked(ctx context.Context, rec *sessionstore.Record) (*TopUpResult, error) {
	intent := rec.RevisionIntent
	var authorization pb.SpendAuthorization
	if err := proto.Unmarshal(intent.AuthorizationBytes, &authorization); err != nil {
		return nil, err
	}
	p := authorization.GetPayload()
	if p == nil || p.GetWholesaleAccountId() != rec.WholesaleAccountID || p.GetSettlementDomainId() != rec.SettlementDomainID || p.GetPredecessorAuthorizationId() != rec.AccountAuthorizationID {
		return nil, fmt.Errorf("persisted revision authority mismatch")
	}
	reservation, ok := new(big.Int).SetString(intent.ReservationWei, 10)
	if !ok || reservation.Sign() < 0 {
		return nil, fmt.Errorf("persisted revision reservation invalid")
	}
	account, ok := e.cfg.Payment.(payment.AccountClient)
	if !ok {
		return nil, fmt.Errorf("account receiver unavailable")
	}
	// Once closing, no new admission/funding is needed. The fence returns any
	// accepted successor, so a lost response can never make us settle its parent.
	if intent.CancelRequested {
		return e.cancelRevisionLocked(ctx, rec, p)
	}
	if rec.Closing() {
		return e.requestRevisionCancellation(ctx, rec, p, "SESSION_CLOSING", "lifecycle", "")
	}
	predecessor, err := account.GetSpendAuthorization(ctx, rec.Sender, rec.AccountAuthorizationID, rec.WholesaleAccountID)
	if err != nil {
		return nil, receiverRevisionFailure("predecessor_lookup", err)
	}
	if predecessor == nil {
		return nil, fmt.Errorf("receiver predecessor status missing")
	}
	switch predecessor.State {
	case int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED):
		if err := reconcileReceiverUsage(rec, predecessor); err != nil {
			return nil, err
		}
		if intent.Decision == nil {
			intent.Decision = e.revisionDecision(rec, "ADMISSION_PENDING", "admission", "")
		}
		intent.Decision.BilledWei, intent.Decision.ActualUnits = predecessor.Billed.String(), predecessor.ActualUnits
		bounded, err := remainingRevisionReservation(p, reservation, predecessor)
		if err != nil {
			return e.requestRevisionCancellation(ctx, rec, p, reservationFailureReason(err), "reservation", "")
		}
		reservation = bounded
		// Includes old sealed intents: repair the derived reservation before any RPC,
		// without changing signed authority, payment bytes, fingerprint or lease.
		if err := e.cfg.Store.Update(rec.SessionID, func(r *sessionstore.Record) error {
			if err := reconcileReceiverUsage(r, predecessor); err != nil {
				return err
			}
			r.RevisionIntent.ReservationWei = reservation.String()
			r.RevisionIntent.Decision = intent.Decision
			r.RevisionIntent.Decision.ReservationWei = reservation.String()
			r.LastRevision = r.RevisionIntent.Decision
			return nil
		}); err != nil {
			return nil, err
		}
	case int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SUPERSEDED):
		// Replay below proves whether this exact successor won; never read the
		// superseded parent's zero reservation as a fresh admission allowance.
	default:
		return e.requestRevisionCancellation(ctx, rec, p, "PREDECESSOR_NOT_ACTIVE", "predecessor_lookup", "")
	}
	admitted, err := account.AdmitAuthorization(ctx, payment.AdmitAuthorizationRequest{AuthorizationBytes: intent.AuthorizationBytes, PaymentBytes: intent.PaymentBytes, Reservation: reservation})
	if err != nil {
		if payment.AdmissionRefused(err) {
			return e.requestRevisionCancellation(ctx, rec, p, payment.SafeAdmissionReason(err), "admission", status.Code(err).String())
		}
		return nil, receiverRevisionFailure("admission", err)
	}
	if admitted == nil || admitted.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) || admitted.Account == nil {
		return nil, fmt.Errorf("receiver did not confirm revision admission")
	}
	usage, err := account.GetSpendAuthorization(ctx, rec.Sender, p.GetAuthorizationId(), rec.WholesaleAccountID)
	if err != nil {
		return nil, err
	}
	return e.commitRevisionLocked(rec, p, usage, admitted.Account, admitted.Credited)
}

func remainingRevisionReservation(p *pb.SpendAuthorizationPayload, requested *big.Int, usage *payment.SpendAuthorizationStatus) (*big.Int, error) {
	if usage.Billed == nil || usage.Billed.Sign() < 0 || usage.ActualUnits >= p.GetMaxTotalUnits() {
		return nil, &revisionFailure{reason: "REVISION_UNITS_EXHAUSTED", stage: "reservation"}
	}
	remaining := new(big.Int).Sub(new(big.Int).SetBytes(p.GetMaxDebitWei().GetValue()), usage.Billed)
	if remaining.Sign() < 0 {
		return nil, &revisionFailure{reason: "REVISION_DEBIT_BELOW_USAGE", stage: "reservation"}
	}
	// For priced remaining work, also bound a loose debit cap by its cost.
	// Cumulative ceilings matter for fractional unit prices.
	price := p.GetAcceptedPrice()
	future := new(big.Int).Sub(payment.BillFor(new(big.Int).SetBytes(price.GetPricePerUnitWei().GetValue()), price.GetUnitsPerPrice(), p.GetMaxTotalUnits()), usage.Billed)
	if future.Sign() < 0 {
		return nil, &revisionFailure{reason: "REVISION_PRICE_BELOW_USAGE", stage: "reservation"}
	}
	if future.Sign() > 0 && future.Cmp(remaining) < 0 {
		remaining.Set(future)
	}
	if requested.Sign() > 0 && requested.Cmp(remaining) < 0 {
		remaining.Set(requested)
	}
	// With a zero remainder, zero on the wire also means zero: remaining
	// units may already be covered by a cumulative ceiling. Free/rounded-flat
	// price curves retain the RPC's default reservation semantics when the
	// signed debit ceiling is loose; unit authority is still bounded above.
	return remaining, nil
}

func (e *Engine) cancelRevisionLocked(ctx context.Context, rec *sessionstore.Record, p *pb.SpendAuthorizationPayload) (*TopUpResult, error) {
	recovery, ok := e.cfg.Payment.(payment.AdmissionRecovery)
	if !ok {
		return nil, fmt.Errorf("receiver admission recovery unavailable")
	}
	result, err := recovery.CancelAuthorizationAdmission(ctx, rec.RevisionIntent.AuthorizationBytes)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("receiver cancellation proof missing")
	}
	if result.Canceled || (result.Authorization != nil && result.Authorization.State == int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_EXPIRED_UNUSED)) {
		basis := "receiver_fenced"
		if !result.Canceled {
			basis = "expired_unused"
		}
		if err := e.prepareRevisionEvidence(rec, false, basis); err != nil {
			return nil, err
		}
		const detail = "authorization revision was not admitted; existing authority retained"
		if err := e.cfg.Store.RefuseRevision(rec.SessionID, rec.RevisionIntent, "refill_refused", detail); err != nil {
			return nil, err
		}
		e.observeRevision(rec.RevisionIntent.Decision)
		return nil, &ProtocolError{Code: "refill_refused", Detail: detail, Revision: rec.RevisionIntent.Decision}
	}
	return e.commitRevisionLocked(rec, p, result.Authorization, result.Account, nil)
}

func (e *Engine) commitRevisionLocked(rec *sessionstore.Record, p *pb.SpendAuthorizationPayload, usage *payment.SpendAuthorizationStatus, account *payment.WholesaleAccount, credited *big.Int) (*TopUpResult, error) {
	if account == nil || account.WholesaleAccountID != rec.WholesaleAccountID || account.SettlementDomainID != rec.SettlementDomainID || !bytesEqual(account.Payer, rec.Sender) || usage == nil || (usage.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) && usage.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED)) {
		return nil, fmt.Errorf("receiver revision authority unresolved")
	}
	if err := e.prepareRevisionEvidence(rec, true, ""); err != nil {
		return nil, err
	}
	intent := rec.RevisionIntent
	err := e.cfg.Store.CommitRevision(rec.SessionID, intent.RequestID, intent.Fingerprint, intent.LeaseExpiresAt, balanceString(account.Available), func(r *sessionstore.Record) error {
		r.AccountAuthorizationID, r.WorkID = p.GetAuthorizationId(), p.GetAuthorizationId()
		r.AuthorizationMaxUnits = p.GetMaxTotalUnits()
		r.AuthorizationMaxDebitWei = new(big.Int).SetBytes(p.GetMaxDebitWei().GetValue()).String()
		if err := reconcileReceiverUsage(r, usage); err != nil {
			return err
		}
		r.LeaseExpiresAt = intent.LeaseExpiresAt
		if credited != nil {
			r.FundedWei = addDecimal(r.FundedWei, credited)
		}
		if usage.State == int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED) && !r.Closing() {
			r.State, r.CloseReason = sessionstore.StateWindingDown, ReasonRecoveryFailed
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	e.observeRevision(intent.Decision)
	return &TopUpResult{Lease: intent.LeaseExpiresAt, Balance: account.Available, Revision: intent.Decision}, nil
}

func (e *Engine) recoverRevisionIntents(ctx context.Context) {
	var ids []string
	if err := e.cfg.Store.ForEach(func(r *sessionstore.Record) error {
		if !r.Terminal() && r.RevisionIntent != nil {
			ids = append(ids, r.SessionID)
		}
		return nil
	}); err != nil {
		e.cfg.Log.Error("revision inventory unavailable", "err", err)
		return
	}
	for _, id := range ids {
		mu := e.sessionMu(id)
		mu.Lock()
		_, err := e.resumeRevisionLocked(ctx, id)
		mu.Unlock()
		if err != nil {
			var refusal *ProtocolError
			if errors.As(err, &refusal) {
				e.cfg.Log.Info("session revision refused; predecessor retained", "session", id, "code", refusal.Code)
			} else {
				e.cfg.Log.Warn("session revision recovery held", "session", id, "err", err)
			}
		}
	}
}

func (e *Engine) requestRevisionCancellation(ctx context.Context, rec *sessionstore.Record, p *pb.SpendAuthorizationPayload, reason, stage, code string) (*TopUpResult, error) {
	if err := e.saveRevisionDecision(rec, e.revisionDecision(rec, reason, stage, code), true); err != nil {
		return nil, err
	}
	return e.cancelRevisionLocked(ctx, rec, p)
}
func reservationFailureReason(err error) string {
	var failure *revisionFailure
	if errors.As(err, &failure) {
		return failure.reason
	}
	return "RESERVATION_INVALID"
}
