package sessionengine

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

// resumeRevisionLocked runs with the session mutex held. A receiver replay can
// complete an earlier accepted revision without creating another reservation.
func (e *Engine) resumeRevisionLocked(ctx context.Context, id string) (*TopUpResult, error) {
	rec, err := e.cfg.Store.Get(id)
	if err != nil {
		return nil, err
	}
	intent := rec.RevisionIntent
	if intent == nil {
		return nil, fmt.Errorf("session revision intent missing")
	}
	var authorization pb.SpendAuthorization
	if err = proto.Unmarshal(intent.AuthorizationBytes, &authorization); err != nil {
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
	admitted, err := account.AdmitAuthorization(ctx, payment.AdmitAuthorizationRequest{AuthorizationBytes: intent.AuthorizationBytes, PaymentBytes: intent.PaymentBytes, Reservation: reservation})
	if err != nil {
		if errors.Is(err, payment.ErrAdmissionStopped) {
			if clearErr := e.cfg.Store.Update(id, func(r *sessionstore.Record) error { r.RevisionIntent = nil; return nil }); clearErr != nil {
				return nil, &RetryableError{Err: clearErr}
			}
			return nil, protoErr("refill_refused", "regional admission is paused; existing work may finish")
		}
		return nil, &RetryableError{Err: fmt.Errorf("revision admission recovery: %w", err)}
	}
	if admitted == nil || admitted.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) || admitted.Account == nil {
		return nil, &RetryableError{Err: fmt.Errorf("receiver did not confirm revision admission")}
	}
	balance := balanceString(admitted.Account.Available)
	err = e.cfg.Store.CommitRevision(id, intent.RequestID, intent.Fingerprint, intent.LeaseExpiresAt, balance, func(r *sessionstore.Record) error {
		r.AccountAuthorizationID, r.WorkID = p.GetAuthorizationId(), p.GetAuthorizationId()
		r.AuthorizationMaxUnits = p.GetMaxTotalUnits()
		r.AuthorizationMaxDebitWei = new(big.Int).SetBytes(p.GetMaxDebitWei().GetValue()).String()
		r.AuthorizationReservedWei = intent.ReservationWei
		if admitted.Reserved != nil {
			r.AuthorizationReservedWei = admitted.Reserved.String()
		}
		r.LeaseExpiresAt = intent.LeaseExpiresAt
		if admitted.Credited != nil {
			r.FundedWei = addDecimal(r.FundedWei, admitted.Credited)
		}
		return nil
	})
	if err != nil {
		return nil, &RetryableError{Err: err}
	}
	return &TopUpResult{Lease: intent.LeaseExpiresAt, Balance: admitted.Account.Available}, nil
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
			e.cfg.Log.Warn("session revision recovery held", "session", id, "err", err)
		}
	}
}
