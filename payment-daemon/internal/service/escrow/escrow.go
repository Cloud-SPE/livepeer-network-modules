// Package escrow computes per-sender ceilings on ticket face_value
// based on on-chain deposit + reserve + currently-pending redemptions.
//
// v0.2 ships an architectural stub: MaxFloat returns deposit + reserve
// without subtracting pending. Plan 0016 wires the real heuristic
// (pending tracking + 3:1 deposit-to-pending ratio) per the operator
// runbook.
package escrow

import (
	"context"
	"math/big"

	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers"
)

// MinDepositPendingRatio is the threshold under which pending
// redemptions are subtracted from MaxFloat. When `deposit /
// pendingAmount >= 3`, MaxFloat ignores pending entirely. Documented in
// `payment-daemon/docs/operator-runbook.md` §"maxFloat".
const MinDepositPendingRatio int64 = 3

// MaxFloat returns the maximum face_value that can be safely committed
// to a single ticket from the given sender. v0.2 stub: returns deposit
// + reserve without pending tracking.
func MaxFloat(ctx context.Context, broker providers.Broker, sender []byte) (*big.Int, error) {
	info, err := broker.GetSenderInfo(ctx, sender)
	if err != nil {
		return nil, err
	}
	max := new(big.Int)
	if info.Deposit != nil {
		max.Add(max, info.Deposit)
	}
	if info.Reserve != nil && info.Reserve.FundsRemaining != nil {
		max.Add(max, info.Reserve.FundsRemaining)
	}
	return max, nil
}
