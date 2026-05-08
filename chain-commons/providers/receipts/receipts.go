// Package receipts provides reorg-aware transaction confirmation tracking.
//
// WaitConfirmed polls for the receipt and tracks the receipt's mined block
// over time; returns when the tx is confirmations blocks deep AND the
// receipt's block hash still matches the canonical chain at that height.
// On reorg-out, returns Receipt{Reorged: true} so the consumer (TxIntent)
// can resubmit.
package receipts

import (
	"context"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/ethereum/go-ethereum/core/types"
)

// Receipts is the confirmation-tracking abstraction.
type Receipts interface {
	// WaitConfirmed blocks until the tx is either confirmed (with the
	// requested number of confirmations) or reorged out. Returns the receipt
	// (Confirmed=true) on success; Receipt{Reorged: true, ...} when the
	// previously-mined tx is no longer in the canonical chain. Returns
	// ctx.Err() on cancellation.
	WaitConfirmed(ctx context.Context, txHash chain.TxHash, confirmations uint64) (*Receipt, error)
}

// Receipt is the confirmation result.
type Receipt struct {
	TxHash      chain.TxHash
	BlockNumber chain.BlockNumber
	BlockHash   chain.TxHash
	Status      uint64 // 0 reverted | 1 success
	GasUsed     uint64
	Logs        []types.Log
	Confirmed   bool // true when confirmations blocks deep
	Reorged     bool // true when a previously-mined tx no longer in canonical chain
}
