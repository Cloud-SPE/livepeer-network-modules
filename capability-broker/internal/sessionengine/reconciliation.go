package sessionengine

import (
	"context"
	"fmt"
	"math"
	"math/big"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// Receiver totals are cumulative across revisions. Never add them as deltas,
// regress a committed bill, or discard an advance whose outcome remains unknown.
func reconcileReceiverUsage(r *sessionstore.Record, usage *payment.SpendAuthorizationStatus) error {
	if usage == nil || usage.Billed == nil || usage.Reserved == nil || usage.Billed.Sign() < 0 || usage.Reserved.Sign() < 0 {
		return fmt.Errorf("receiver accounting state missing or invalid")
	}
	billed, ok := new(big.Int).SetString(r.BilledWei, 10)
	if !ok {
		billed = new(big.Int)
	}
	if usage.ActualUnits < r.DebitedTotal || usage.Billed.Cmp(billed) < 0 || usage.SettlementSeq < r.DebitSeq {
		return fmt.Errorf("receiver accounting state regressed")
	}
	if r.PendingDebitSeq != 0 && usage.SettlementSeq < r.PendingDebitSeq {
		return fmt.Errorf("usage advance remains unresolved")
	}
	maxDebit, ok := new(big.Int).SetString(r.AuthorizationMaxDebitWei, 10)
	if !ok || usage.ActualUnits > r.AuthorizationMaxUnits || usage.Billed.Cmp(maxDebit) > 0 {
		return fmt.Errorf("receiver usage exceeds authorization")
	}
	r.DebitedTotal, r.BilledWei, r.DebitSeq = usage.ActualUnits, usage.Billed.String(), usage.SettlementSeq
	if r.ClaimedTotal < usage.ActualUnits {
		r.ClaimedTotal = usage.ActualUnits
	}
	r.AuthorizationReservedWei = usage.Reserved.String()
	r.PendingDebitSeq = 0
	return nil
}

// Settle with the receiver's high watermark. A lost settlement response replays
// the same sequence rather than incrementing again or lowering cumulative units.
func (e *Engine) settleSessionAuthorizationLocked(ctx context.Context, rec *sessionstore.Record, ac payment.AccountClient) (*payment.SettleAuthorizationResult, error) {
	usage, err := ac.GetSpendAuthorization(ctx, rec.Sender, rec.AccountAuthorizationID, rec.WholesaleAccountID)
	if err != nil {
		return nil, err
	}
	if usage == nil || (usage.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) && usage.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED)) {
		return nil, fmt.Errorf("receiver settlement authority unresolved")
	}
	if err := reconcileReceiverUsage(rec, usage); err != nil {
		return nil, err
	}
	if err := e.cfg.Store.Update(rec.SessionID, func(r *sessionstore.Record) error { return reconcileReceiverUsage(r, usage) }); err != nil {
		return nil, err
	}
	seq := usage.SettlementSeq
	if usage.State == int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) {
		if seq == math.MaxUint64 {
			return nil, fmt.Errorf("receiver settlement sequence exhausted")
		}
		seq++
	}
	settled, err := ac.SettleAuthorization(ctx, payment.SettleAuthorizationRequest{WholesaleAccountID: rec.WholesaleAccountID, Payer: rec.Sender, AuthorizationID: rec.AccountAuthorizationID, ActualUnits: usage.ActualUnits, SettlementSeq: seq})
	if err != nil {
		return nil, err
	}
	if settled == nil || settled.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED) || settled.Billed == nil || settled.Billed.Cmp(usage.Billed) != 0 {
		return nil, fmt.Errorf("receiver returned inconsistent settlement")
	}
	return settled, nil
}
