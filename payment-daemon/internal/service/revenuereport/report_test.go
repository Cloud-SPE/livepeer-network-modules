package revenuereport

import (
	"context"
	"encoding/binary"
	"errors"
	"math/big"
	"path/filepath"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/types"
	"github.com/ethereum/go-ethereum/common"
)

type observer struct {
	entries               map[common.Hash]*types.RedemptionInclusion
	stale, reorg, pending bool
}

func (o *observer) RevenueCoverage(context.Context) (*types.RevenueCoverage, error) {
	if o.stale {
		return nil, errors.New("stale head")
	}
	return &types.RevenueCoverage{Head: 2000, HeadHash: make([]byte, 32), FinalizedBlock: 1996, FinalizedBlockHash: make([]byte, 32), CompleteThroughRound: 101, ObservedAt: time.Now()}, nil
}
func (o *observer) VerifyRevenueCoverage(context.Context, *types.RevenueCoverage) error {
	if o.reorg {
		return errors.New("reorg")
	}
	return nil
}
func (o *observer) InclusionForTransaction(_ context.Context, h common.Hash) (*types.RedemptionInclusion, error) {
	e := o.entries[h]
	if e == nil {
		return nil, errors.New("missing receipt")
	}
	return e, nil
}
func (o *observer) ConfirmedInclusion(context.Context, []byte) (*types.RedemptionInclusion, error) {
	if o.pending {
		return nil, errors.New("confirmation outstanding")
	}
	return nil, nil
}

func TestCompleteRevenueOver500SurvivesRestartAndHoldsUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	chain := &observer{entries: map[common.Hash]*types.RedemptionInclusion{}}
	for i := 1; i <= 601; i++ {
		hash := make([]byte, 32)
		binary.BigEndian.PutUint64(hash[24:], uint64(i))
		tx := common.BytesToHash(hash)
		evidence := &types.RedemptionInclusion{TxHash: tx.Bytes(), BlockNumber: uint64(i + 100), BlockHash: common.HexToHash("0xabc").Bytes(), Round: 100, ObservedHead: 2000, CheckedAt: time.Now()}
		ticket := &store.SignedTicket{FaceValue: big.NewInt(100), CreationRound: 99}
		if _, err := st.EnqueueRedemption(hash, ticket); err != nil {
			t.Fatal(err)
		}
		if err := st.MarkRedeemedWithInclusion(hash, ticket, evidence); err != nil {
			t.Fatal(err)
		}
		chain.entries[tx] = evidence
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	reporter := Reporter{Store: st, Chain: chain}
	result, err := reporter.Round(context.Background(), 100)
	if err != nil || !result.Complete || result.ConfirmedTicketCount != 601 || new(big.Int).SetBytes(result.ConfirmedRevenueWei).Int64() != 60100 {
		t.Fatalf("report=%+v err=%v", result, err)
	}
	again, err := reporter.Round(context.Background(), 100)
	if err != nil || string(again.InclusionDigest) != string(result.InclusionDigest) {
		t.Fatal("retry changed inclusion digest")
	}
	zero, err := reporter.Round(context.Background(), 101)
	if err != nil || !zero.Complete || zero.ConfirmedTicketCount != 0 {
		t.Fatal("proven zero not complete")
	}
	chain.stale = true
	held, err := reporter.Round(context.Background(), 100)
	if err != nil || held.Complete || held.IncompleteReason == "" {
		t.Fatal("stale source accepted")
	}
	chain.stale = false
	chain.reorg = true
	held, err = reporter.Round(context.Background(), 100)
	if err != nil || held.Complete {
		t.Fatal("reorg accepted")
	}
	chain.reorg = false
	chain.pending = true
	hash := make([]byte, 32)
	hash[0] = 99
	if _, err := st.EnqueueRedemption(hash, &store.SignedTicket{FaceValue: big.NewInt(100), CreationRound: 99}); err != nil {
		t.Fatal(err)
	}
	held, err = reporter.Round(context.Background(), 100)
	if err != nil || held.Complete {
		t.Fatal("pending confirmation accepted")
	}
}

func TestLegacyUnknownRedemptionIsNotCompleteZero(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	hash := make([]byte, 32)
	hash[0] = 1
	ticket := &store.SignedTicket{FaceValue: big.NewInt(100), CreationRound: 99}
	if err := st.MarkDrained(hash, ticket, 100, "used"); err != nil {
		t.Fatal(err)
	}
	result, err := (Reporter{Store: st, Chain: &observer{}}).Round(context.Background(), 100)
	if err != nil || result.Complete || result.IncompleteReason == "" {
		t.Fatalf("unknown used history %+v %v", result, err)
	}
}
