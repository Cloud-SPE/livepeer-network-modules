package chaintesting

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

// TestFakeRPC_AllMethods exercises every FakeRPC method with both default
// behaviour and an injected error to keep coverage above the 75% gate.
func TestFakeRPC_AllMethods(t *testing.T) {
	r := NewFakeRPC()
	ctx := context.Background()
	addr := chain.Address{0x01}
	hash := chain.TxHash{0x02}

	// Defaults must succeed.
	if _, err := r.CallContract(ctx, ethereum.CallMsg{}, nil); err != nil {
		t.Errorf("CallContract: %v", err)
	}
	if _, err := r.PendingCallContract(ctx, ethereum.CallMsg{}); err != nil {
		t.Errorf("PendingCallContract: %v", err)
	}
	if _, err := r.CodeAt(ctx, addr, nil); err != nil {
		t.Errorf("CodeAt: %v", err)
	}
	if err := r.SendTransaction(ctx, types.NewTx(&types.LegacyTx{})); err != nil {
		t.Errorf("SendTransaction: %v", err)
	}
	if _, _, err := r.TransactionByHash(ctx, hash); err == nil {
		t.Errorf("TransactionByHash default should err (not found)")
	}
	if _, err := r.TransactionReceipt(ctx, hash); err != ethereum.NotFound {
		t.Errorf("TransactionReceipt default = %v", err)
	}
	if _, err := r.BlockByNumber(ctx, nil); err != ethereum.NotFound {
		t.Errorf("BlockByNumber default = %v", err)
	}
	if _, err := r.HeaderByNumber(ctx, nil); err != nil {
		t.Errorf("HeaderByNumber: %v", err)
	}
	if _, err := r.FilterLogs(ctx, ethereum.FilterQuery{}); err != nil {
		t.Errorf("FilterLogs: %v", err)
	}
	if _, err := r.PendingNonceAt(ctx, addr); err != nil {
		t.Errorf("PendingNonceAt: %v", err)
	}
	if _, err := r.BalanceAt(ctx, addr, nil); err != nil {
		t.Errorf("BalanceAt: %v", err)
	}
	if _, err := r.SuggestGasPrice(ctx); err != nil {
		t.Errorf("SuggestGasPrice: %v", err)
	}
	if _, err := r.SuggestGasTipCap(ctx); err != nil {
		t.Errorf("SuggestGasTipCap: %v", err)
	}
	if _, err := r.ChainID(ctx); err != nil {
		t.Errorf("ChainID: %v", err)
	}
}

// TestFakeRPC_AllMethodsWithFunc verifies that the *Func overrides are
// respected for every method.
func TestFakeRPC_AllMethodsWithFunc(t *testing.T) {
	r := NewFakeRPC()
	ctx := context.Background()
	want := errors.New("injected")

	r.CallContractFunc = func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		return []byte{1}, nil
	}
	r.PendingCallContractFunc = func(_ context.Context, _ ethereum.CallMsg) ([]byte, error) {
		return []byte{2}, nil
	}
	r.CodeAtFunc = func(_ context.Context, _ chain.Address, _ *big.Int) ([]byte, error) {
		return []byte{3}, nil
	}
	r.SendTransactionFunc = func(_ context.Context, _ *types.Transaction) error { return want }
	r.TransactionByHashFunc = func(_ context.Context, _ chain.TxHash) (*types.Transaction, bool, error) {
		return nil, true, nil
	}
	r.TransactionReceiptFunc = func(_ context.Context, _ chain.TxHash) (*types.Receipt, error) {
		return &types.Receipt{Status: 1}, nil
	}
	r.BlockByNumberFunc = func(_ context.Context, _ *big.Int) (*types.Block, error) {
		return nil, want
	}
	r.HeaderByNumberFunc = func(_ context.Context, _ *big.Int) (*types.Header, error) {
		return &types.Header{Number: big.NewInt(99)}, nil
	}
	r.FilterLogsFunc = func(_ context.Context, _ ethereum.FilterQuery) ([]types.Log, error) {
		return []types.Log{{Address: chain.Address{0x42}}}, nil
	}
	r.PendingNonceAtFunc = func(_ context.Context, _ chain.Address) (uint64, error) { return 7, nil }
	r.BalanceAtFunc = func(_ context.Context, _ chain.Address, _ *big.Int) (*big.Int, error) {
		return big.NewInt(99), nil
	}
	r.SuggestGasPriceFunc = func(_ context.Context) (*big.Int, error) { return big.NewInt(50), nil }
	r.SuggestGasTipCapFunc = func(_ context.Context) (*big.Int, error) { return big.NewInt(5), nil }
	r.ChainIDFunc = func(_ context.Context) (chain.ChainID, error) { return 1337, nil }

	if v, _ := r.CallContract(ctx, ethereum.CallMsg{}, nil); v[0] != 1 {
		t.Errorf("CallContract override not respected")
	}
	if v, _ := r.PendingCallContract(ctx, ethereum.CallMsg{}); v[0] != 2 {
		t.Errorf("PendingCallContract override not respected")
	}
	if v, _ := r.CodeAt(ctx, chain.Address{}, nil); v[0] != 3 {
		t.Errorf("CodeAt override not respected")
	}
	if err := r.SendTransaction(ctx, types.NewTx(&types.LegacyTx{})); err != want {
		t.Errorf("SendTransaction override not respected: %v", err)
	}
	if _, isPending, _ := r.TransactionByHash(ctx, chain.TxHash{}); !isPending {
		t.Errorf("TransactionByHash override not respected")
	}
	if rec, _ := r.TransactionReceipt(ctx, chain.TxHash{}); rec.Status != 1 {
		t.Errorf("TransactionReceipt override not respected")
	}
	if _, err := r.BlockByNumber(ctx, nil); err != want {
		t.Errorf("BlockByNumber override not respected")
	}
	if h, _ := r.HeaderByNumber(ctx, nil); h.Number.Int64() != 99 {
		t.Errorf("HeaderByNumber override not respected")
	}
	if logs, _ := r.FilterLogs(ctx, ethereum.FilterQuery{}); logs[0].Address != (chain.Address{0x42}) {
		t.Errorf("FilterLogs override not respected")
	}
	if n, _ := r.PendingNonceAt(ctx, chain.Address{}); n != 7 {
		t.Errorf("PendingNonceAt override not respected")
	}
	if v, _ := r.BalanceAt(ctx, chain.Address{}, nil); v.Int64() != 99 {
		t.Errorf("BalanceAt override not respected")
	}
	if v, _ := r.SuggestGasPrice(ctx); v.Int64() != 50 {
		t.Errorf("SuggestGasPrice override not respected")
	}
	if v, _ := r.SuggestGasTipCap(ctx); v.Int64() != 5 {
		t.Errorf("SuggestGasTipCap override not respected")
	}
	if id, _ := r.ChainID(ctx); id != 1337 {
		t.Errorf("ChainID override not respected")
	}
}
