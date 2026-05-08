// Package reorg provides a reorg-aware receipts.Receipts implementation.
//
// WaitConfirmed polls TransactionReceipt; once the tx is mined, polls
// HeaderByNumber at the receipt's mined block to detect reorg-out (block
// hash mismatch); transitions to Confirmed only after Confirmations
// blocks deeper.
package reorg

import (
	"context"
	"errors"
	"math/big"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	cerrors "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/errors"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/receipts"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc"
	"github.com/ethereum/go-ethereum"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

// derefLogs converts go-ethereum's []*types.Log to []types.Log (which is
// what providers/receipts.Receipt.Logs expects — receipts are passed by
// value in chain-commons interfaces).
func derefLogs(in []*ethtypes.Log) []ethtypes.Log {
	if len(in) == 0 {
		return nil
	}
	out := make([]ethtypes.Log, len(in))
	for i, p := range in {
		if p != nil {
			out[i] = *p
		}
	}
	return out
}

// Options wires a reorg-aware Receipts.
type Options struct {
	RPC          rpc.RPC
	Poll         time.Duration // how often to poll for receipt + head; default 5s
	Clock        clock.Clock
}

// New returns a reorg-aware receipts.Receipts. RPC is required.
func New(opts Options) (receipts.Receipts, error) {
	if opts.RPC == nil {
		return nil, errors.New("reorg receipts: RPC is required")
	}
	if opts.Poll == 0 {
		opts.Poll = 5 * time.Second
	}
	if opts.Clock == nil {
		opts.Clock = clock.System()
	}
	return &reorgReceipts{rpc: opts.RPC, poll: opts.Poll, clock: opts.Clock}, nil
}

type reorgReceipts struct {
	rpc   rpc.RPC
	poll  time.Duration
	clock clock.Clock
}

// WaitConfirmed implements receipts.Receipts.
func (r *reorgReceipts) WaitConfirmed(ctx context.Context, txHash chain.TxHash, confirmations uint64) (*receipts.Receipt, error) {
	for {
		// Step 1: poll for receipt until it's mined.
		receipt, err := r.rpc.TransactionReceipt(ctx, txHash)
		if err != nil {
			if !errors.Is(err, ethereum.NotFound) {
				return nil, cerrors.Classify(err)
			}
			// Not yet mined; sleep and retry.
			if err := r.clock.Sleep(ctx, r.poll); err != nil {
				return nil, err
			}
			continue
		}

		// Step 2: receipt found. If status=0 (reverted), surface immediately.
		if receipt.Status == 0 {
			return &receipts.Receipt{
				TxHash:      txHash,
				BlockNumber: chain.BlockNumber(receipt.BlockNumber.Uint64()),
				BlockHash:   receipt.BlockHash,
				Status:      receipt.Status,
				GasUsed:     receipt.GasUsed,
				Logs:        derefLogs(receipt.Logs),
				Confirmed:   false,
			}, nil
		}

		// Step 3: wait for confirmations + reorg detection.
		minedBlock := receipt.BlockNumber
		minedHash := receipt.BlockHash
		for {
			head, err := r.rpc.HeaderByNumber(ctx, nil)
			if err != nil {
				if err := r.clock.Sleep(ctx, r.poll); err != nil {
					return nil, err
				}
				continue
			}
			depth := new(big.Int).Sub(head.Number, minedBlock)
			if depth.Sign() < 0 {
				// Head is below mined block — chain reorged backward.
				return &receipts.Receipt{TxHash: txHash, Reorged: true}, nil
			}
			if depth.Uint64() >= confirmations {
				// Verify the receipt's block hash still matches at that height.
				canonical, err := r.rpc.HeaderByNumber(ctx, minedBlock)
				if err != nil {
					if err := r.clock.Sleep(ctx, r.poll); err != nil {
						return nil, err
					}
					continue
				}
				if canonical.Hash() != minedHash {
					return &receipts.Receipt{TxHash: txHash, Reorged: true}, nil
				}
				return &receipts.Receipt{
					TxHash:      txHash,
					BlockNumber: chain.BlockNumber(minedBlock.Uint64()),
					BlockHash:   minedHash,
					Status:      receipt.Status,
					GasUsed:     receipt.GasUsed,
					Logs:        derefLogs(receipt.Logs),
					Confirmed:   true,
				}, nil
			}
			// Not deep enough yet; sleep and re-poll.
			if err := r.clock.Sleep(ctx, r.poll); err != nil {
				return nil, err
			}
		}
	}
}
