package ticketbroker

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"time"

	chainstore "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/txintent"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/types"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// ConfirmedInclusion recovers a prior intent before settlement applies age/used
// checks. A confirmed transaction awaiting accounting is never drained as used.
// Nil means no successful prior intent; in-flight or uncertain history is held.
func (b *Broker) ConfirmedInclusion(ctx context.Context, ticketHash []byte) (*types.RedemptionInclusion, error) {
	if b.intents == nil {
		return nil, fmt.Errorf("redemption intent manager unavailable")
	}
	intent, err := b.intents.Status(ctx, txintent.ComputeID(IntentKind, ticketHash))
	if errors.Is(err, chainstore.ErrNotFound) {
		used, lookupErr := b.IsUsedTicket(ctx, ticketHash)
		if lookupErr != nil {
			return nil, lookupErr
		}
		if used {
			return nil, fmt.Errorf("used ticket lacks local intent inclusion history")
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if intent.Status == txintent.StatusFailed {
		if len(intent.Attempts) > 0 {
			return nil, fmt.Errorf("failed intent has broadcast attempts requiring audited reconciliation")
		}
		return nil, nil
	}
	if intent.Status != txintent.StatusConfirmed {
		return nil, fmt.Errorf("redemption intent not yet confirmed")
	}
	attempt := intent.CurrentAttempt()
	if attempt == nil {
		return nil, fmt.Errorf("confirmed intent has no transaction")
	}
	return b.InclusionForTransaction(ctx, attempt.SignedTxHash)
}

// InclusionForTransaction rechecks canonicality and confirmations and reads
// currentRound at the receipt's historical block. Archive RPC failure is unknown
// evidence; it must never be replaced with the latest clock's round.
func (b *Broker) InclusionForTransaction(ctx context.Context, hash common.Hash) (*types.RedemptionInclusion, error) {
	if b.cfg.RoundsManager == (common.Address{}) {
		return nil, fmt.Errorf("rounds manager unavailable")
	}
	receipt, err := b.client.TransactionReceipt(ctx, hash)
	if err != nil {
		return nil, err
	}
	if receipt == nil || receipt.BlockNumber == nil || receipt.Status != ethtypes.ReceiptStatusSuccessful || receipt.TxHash != hash || receipt.BlockHash == (common.Hash{}) {
		return nil, fmt.Errorf("successful inclusion receipt unavailable")
	}
	head, err := b.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, err
	}
	block := receipt.BlockNumber.Uint64()
	if !receipt.BlockNumber.IsUint64() || head == nil || head.Number == nil || !head.Number.IsUint64() || head.Number.Uint64() < block || head.Number.Uint64()-block < b.cfg.Confirmations {
		return nil, fmt.Errorf("inclusion lacks confirmations")
	}
	canonical, err := b.client.HeaderByNumber(ctx, receipt.BlockNumber)
	if err != nil {
		return nil, err
	}
	if canonical == nil || canonical.Hash() != receipt.BlockHash {
		return nil, fmt.Errorf("redemption inclusion reorged")
	}
	data, err := b.client.CallContract(ctx, ethereum.CallMsg{To: &b.cfg.RoundsManager, Data: crypto.Keccak256([]byte("currentRound()"))[:4]}, receipt.BlockNumber)
	if err != nil {
		return nil, fmt.Errorf("historical inclusion round: %w", err)
	}
	round := new(big.Int).SetBytes(data)
	if len(data) != 32 || !round.IsInt64() || round.Sign() <= 0 {
		return nil, fmt.Errorf("invalid historical round")
	}
	// Catch a reorg that crossed the historical call.
	canonical, err = b.client.HeaderByNumber(ctx, receipt.BlockNumber)
	if err != nil {
		return nil, err
	}
	if canonical == nil || canonical.Hash() != receipt.BlockHash {
		return nil, fmt.Errorf("redemption inclusion changed during observation")
	}
	return &types.RedemptionInclusion{TxHash: hash.Bytes(), BlockNumber: block, BlockHash: receipt.BlockHash.Bytes(), Round: round.Int64(), ObservedHead: head.Number.Uint64(), ObservedHeadHash: head.Hash().Bytes(), CheckedAt: time.Now().UTC()}, nil
}

// RevenueCoverage proves a confirmed chain prefix is beyond the requested
// round. A responsive but stale RPC is not evidence of complete revenue.
func (b *Broker) RevenueCoverage(ctx context.Context) (*types.RevenueCoverage, error) {
	head, err := b.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if head == nil || head.Number == nil || !head.Number.IsUint64() || head.Number.Uint64() < b.cfg.Confirmations || head.Time > uint64(now.Add(time.Minute).Unix()) || head.Time < uint64(now.Add(-2*time.Minute).Unix()) {
		return nil, fmt.Errorf("chain head stale or invalid")
	}
	height := head.Number.Uint64() - b.cfg.Confirmations
	block, err := b.client.HeaderByNumber(ctx, new(big.Int).SetUint64(height))
	if err != nil {
		return nil, err
	}
	if block == nil || block.Number == nil || block.Number.Uint64() != height {
		return nil, fmt.Errorf("finalized block unavailable")
	}
	data, err := b.client.CallContract(ctx, ethereum.CallMsg{To: &b.cfg.RoundsManager, Data: crypto.Keccak256([]byte("currentRound()"))[:4]}, block.Number)
	if err != nil {
		return nil, err
	}
	round := new(big.Int).SetBytes(data)
	if len(data) != 32 || !round.IsInt64() || round.Sign() <= 0 {
		return nil, fmt.Errorf("finalized round unavailable")
	}
	again, err := b.client.HeaderByNumber(ctx, block.Number)
	if err != nil {
		return nil, err
	}
	if again == nil || again.Hash() != block.Hash() {
		return nil, fmt.Errorf("finalized chain changed during observation")
	}
	return &types.RevenueCoverage{Head: head.Number.Uint64(), HeadHash: head.Hash().Bytes(), FinalizedBlock: height, FinalizedBlockHash: block.Hash().Bytes(), CompleteThroughRound: round.Int64() - 1, ObservedAt: now}, nil
}

func (b *Broker) VerifyRevenueCoverage(ctx context.Context, coverage *types.RevenueCoverage) error {
	if coverage == nil || time.Since(coverage.ObservedAt) > 2*time.Minute {
		return fmt.Errorf("coverage observation expired")
	}
	head, err := b.client.HeaderByNumber(ctx, new(big.Int).SetUint64(coverage.Head))
	if err != nil {
		return err
	}
	if head == nil || head.Hash() != common.BytesToHash(coverage.HeadHash) {
		return fmt.Errorf("observed chain prefix reorged")
	}
	return nil
}
