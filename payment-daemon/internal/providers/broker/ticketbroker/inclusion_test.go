package ticketbroker

import (
	"context"
	"errors"
	chainstore "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/txintent"
	"math/big"
	"testing"
	"time"

	chaintesting "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
)

func TestInclusionUsesHistoricalRoundAndRejectsUncertainEvidence(t *testing.T) {
	rpc := chaintesting.NewFakeRPC()
	block := &ethtypes.Header{Number: big.NewInt(100), Time: 1000}
	head := &ethtypes.Header{Number: big.NewInt(110), Time: 1100}
	hash := common.HexToHash("0x1234")
	receipt := &ethtypes.Receipt{TxHash: hash, BlockNumber: big.NewInt(100), BlockHash: block.Hash(), Status: 1}
	rpc.TransactionReceiptFunc = func(context.Context, common.Hash) (*ethtypes.Receipt, error) { return receipt, nil }
	reorg := false
	unavailable := false
	rpc.HeaderByNumberFunc = func(_ context.Context, n *big.Int) (*ethtypes.Header, error) {
		if n == nil {
			return head, nil
		}
		if reorg {
			return &ethtypes.Header{Number: big.NewInt(100), Time: 999}, nil
		}
		return block, nil
	}
	rpc.CallContractFunc = func(_ context.Context, msg ethereum.CallMsg, n *big.Int) ([]byte, error) {
		if unavailable {
			return nil, errors.New("archive unavailable")
		}
		if n == nil || n.Int64() != 100 || msg.To == nil || *msg.To != claimantAddr {
			t.Fatal("round read at wrong block/contract")
		}
		return big.NewInt(321).FillBytes(make([]byte, 32)), nil
	}
	b, err := New(Config{Address: contractAddr, RoundsManager: claimantAddr, Confirmations: 4}, rpc, nil)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := b.InclusionForTransaction(context.Background(), hash)
	if err != nil || evidence.Round != 321 || evidence.BlockNumber != 100 || evidence.ObservedHead != 110 {
		t.Fatalf("inclusion=%+v err=%v", evidence, err)
	}
	unavailable = true
	if _, err := b.InclusionForTransaction(context.Background(), hash); err == nil {
		t.Fatal("archive failure used latest round")
	}
	unavailable = false
	reorg = true
	if _, err := b.InclusionForTransaction(context.Background(), hash); err == nil {
		t.Fatal("reorg accepted")
	}
	reorg = false
	head.Number = big.NewInt(102)
	if _, err := b.InclusionForTransaction(context.Background(), hash); err == nil {
		t.Fatal("insufficient confirmations accepted")
	}
}

func TestCoverageRejectsResponsiveStaleRPC(t *testing.T) {
	rpc := chaintesting.NewFakeRPC()
	head := &ethtypes.Header{Number: big.NewInt(100), Time: uint64(time.Now().Add(-time.Hour).Unix())}
	rpc.HeaderByNumberFunc = func(context.Context, *big.Int) (*ethtypes.Header, error) { return head, nil }
	b, err := New(Config{Address: contractAddr, RoundsManager: claimantAddr, Confirmations: 4}, rpc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.RevenueCoverage(context.Background()); err == nil {
		t.Fatal("stale but responsive head counted complete")
	}
}

func TestCoverageAndIntentRecovery(t *testing.T) {
	rpc := chaintesting.NewFakeRPC()
	head := &ethtypes.Header{Number: big.NewInt(110), Time: uint64(time.Now().Unix())}
	block := &ethtypes.Header{Number: big.NewInt(100), Time: head.Time - 10}
	rpc.HeaderByNumberFunc = func(_ context.Context, n *big.Int) (*ethtypes.Header, error) {
		if n == nil || n.Uint64() == 110 {
			return head, nil
		}
		if n.Uint64() == 100 {
			return block, nil
		}
		return &ethtypes.Header{Number: new(big.Int).Set(n), Time: head.Time - 4}, nil
	}
	rpc.CallContractFunc = func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		if msg.To != nil && *msg.To == contractAddr {
			return make([]byte, 32), nil
		}
		return big.NewInt(322).FillBytes(make([]byte, 32)), nil
	}
	hash := common.HexToHash("0x1234")
	rpc.TransactionReceiptFunc = func(context.Context, common.Hash) (*ethtypes.Receipt, error) {
		return &ethtypes.Receipt{TxHash: hash, BlockNumber: big.NewInt(100), BlockHash: block.Hash(), Status: 1}, nil
	}
	intents := &fakeIntents{}
	b, err := New(Config{Address: contractAddr, RoundsManager: claimantAddr, Confirmations: 4}, rpc, intents)
	if err != nil {
		t.Fatal(err)
	}
	coverage, err := b.RevenueCoverage(context.Background())
	if err != nil || coverage.CompleteThroughRound != 321 || coverage.FinalizedBlock != 106 {
		t.Fatalf("coverage %+v %v", coverage, err)
	}
	if err := b.VerifyRevenueCoverage(context.Background(), coverage); err != nil {
		t.Fatal(err)
	}
	coverage.ObservedAt = time.Now().Add(-time.Hour)
	if err := b.VerifyRevenueCoverage(context.Background(), coverage); err == nil {
		t.Fatal("expired coverage accepted")
	}
	if _, err := b.ConfirmedInclusion(context.Background(), hash.Bytes()); err == nil {
		t.Fatal("pending intent accepted")
	}
	intents.statusFn = func(txintent.IntentID) (txintent.TxIntent, error) { return txintent.TxIntent{}, chainstore.ErrNotFound }
	if got, err := b.ConfirmedInclusion(context.Background(), hash.Bytes()); err != nil || got != nil {
		t.Fatalf("unused/no intent %+v %v", got, err)
	}
	intents.statusFn = func(txintent.IntentID) (txintent.TxIntent, error) {
		return txintent.TxIntent{Status: txintent.StatusConfirmed, Attempts: []txintent.IntentAttempt{{SignedTxHash: hash}}}, nil
	}
	evidence, err := b.ConfirmedInclusion(context.Background(), hash.Bytes())
	if err != nil || evidence.Round != 322 {
		t.Fatalf("recover %+v %v", evidence, err)
	}
	intents.statusFn = func(txintent.IntentID) (txintent.TxIntent, error) {
		return txintent.TxIntent{Status: txintent.StatusFailed}, nil
	}
	if got, err := b.ConfirmedInclusion(context.Background(), hash.Bytes()); err != nil || got != nil {
		t.Fatal("unsent failed intent cannot retry")
	}
}
