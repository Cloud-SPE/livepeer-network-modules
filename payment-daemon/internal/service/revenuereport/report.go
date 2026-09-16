// Package revenuereport produces complete receiver-ledger revenue observations.
// It reads a single durable history snapshot and revalidates chain evidence;
// reporting never redeems tickets or alters receiver balances.
package revenuereport

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/types"
	"github.com/ethereum/go-ethereum/common"
)

type Observer interface {
	RevenueCoverage(context.Context) (*types.RevenueCoverage, error)
	VerifyRevenueCoverage(context.Context, *types.RevenueCoverage) error
	InclusionForTransaction(context.Context, common.Hash) (*types.RedemptionInclusion, error)
	ConfirmedInclusion(context.Context, []byte) (*types.RedemptionInclusion, error)
}

type Reporter struct {
	Store *store.Store
	Chain Observer
}

type entry struct {
	TicketHash  []byte `json:"ticket_hash"`
	TxHash      []byte `json:"tx_hash"`
	BlockNumber uint64 `json:"block_number"`
	BlockHash   []byte `json:"block_hash"`
	Round       int64  `json:"round"`
	Value       string `json:"value_wei"`
}

func (r Reporter) Round(ctx context.Context, round int64) (*pb.GetRoundRevenueResponse, error) {
	result := &pb.GetRoundRevenueResponse{RoundId: round}
	hold := func(reason string) (*pb.GetRoundRevenueResponse, error) {
		result.IncompleteReason = reason
		return result, nil
	}
	if r.Chain == nil {
		return hold("chain observation unavailable")
	}
	coverage, err := r.Chain.RevenueCoverage(ctx)
	if err != nil {
		return hold("chain coverage: " + err.Error())
	}
	if coverage == nil || coverage.CompleteThroughRound < round {
		return hold("round not yet covered by confirmed chain prefix")
	}
	result.ObservedHead = coverage.Head
	result.ObservedHeadHash = coverage.HeadHash
	result.FinalizedBlock = coverage.FinalizedBlock
	result.FinalizedBlockHash = coverage.FinalizedBlockHash
	result.CompleteThroughRound = coverage.CompleteThroughRound
	result.ObservedAt = coverage.ObservedAt.Format(time.RFC3339Nano)
	pending, redeemed, legacy, err := r.Store.RedemptionHistory()
	if err != nil {
		return nil, err
	}
	if legacy {
		return hold("legacy redemption history lacks source evidence; restore or backfill before closing")
	}
	entries := []entry{}
	add := func(hash []byte, value string, evidence *types.RedemptionInclusion) error {
		if evidence == nil {
			return fmt.Errorf("inclusion unavailable")
		}
		if evidence.Round != round {
			return nil
		}
		amount, ok := new(big.Int).SetString(value, 10)
		if !ok || amount.Sign() < 0 || len(hash) != 32 || len(evidence.TxHash) != 32 || len(evidence.BlockHash) != 32 || evidence.BlockNumber > coverage.FinalizedBlock {
			return fmt.Errorf("invalid inclusion or amount")
		}
		entries = append(entries, entry{hash, evidence.TxHash, evidence.BlockNumber, evidence.BlockHash, evidence.Round, amount.String()})
		return nil
	}
	for _, record := range redeemed {
		if record.CreationRound > round {
			continue
		}
		if !record.ConfirmedOnChain {
			switch record.DrainReason {
			case "expired", "face_value_too_low", "reverted":
				continue
			}
			return hold("unresolved drained or already-used ticket history")
		}
		evidence, err := r.Chain.InclusionForTransaction(ctx, common.BytesToHash(record.TxHash))
		if err != nil {
			return hold("redemption inclusion: " + err.Error())
		}
		if record.Inclusion == nil {
			return hold("legacy confirmed redemption needs durable inclusion backfill")
		}
		if evidence == nil || evidence.Round != record.Inclusion.Round || !bytes.Equal(evidence.BlockHash, record.Inclusion.BlockHash) {
			return hold("previously recorded inclusion changed; audited recovery required")
		}
		if err := add(record.TicketHash, record.FaceValueWei, evidence); err != nil {
			return hold(err.Error())
		}
	}
	for _, record := range pending {
		if record.Ticket == nil {
			return hold("pending ticket history incomplete")
		}
		if record.Ticket.CreationRound > round {
			continue
		}
		evidence, err := r.Chain.ConfirmedInclusion(ctx, record.Hash)
		if err != nil {
			return hold("pending redemption confirmation: " + err.Error())
		}
		// No prior intent means this ticket can only redeem after our observed
		// confirmed prefix. Its later inclusion belongs to a later round.
		if evidence == nil {
			continue
		}
		// Settlement must durably record the evidence before a report is complete.
		return hold("confirmed redemption awaits durable settlement catch-up")
	}
	if err := r.Chain.VerifyRevenueCoverage(ctx, coverage); err != nil {
		return hold("coverage changed: " + err.Error())
	}
	sort.Slice(entries, func(i, j int) bool { return bytes.Compare(entries[i].TicketHash, entries[j].TicketHash) < 0 })
	total := new(big.Int)
	for _, e := range entries {
		amount, _ := new(big.Int).SetString(e.Value, 10)
		total.Add(total, amount)
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(raw)
	result.ConfirmedRevenueWei = total.Bytes()
	result.ConfirmedTicketCount = uint64(len(entries))
	result.InclusionDigest = digest[:]
	result.Complete = true
	return result, nil
}
