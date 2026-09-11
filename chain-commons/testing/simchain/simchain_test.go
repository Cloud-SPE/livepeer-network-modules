package simchain_test

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	chaintest "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing/simchain"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
)

// answer42 is a hand-assembled contract whose runtime returns 32 bytes
// holding 42 for any call. Init code copies the 10-byte runtime out of
// itself and returns it.
//
//	init:    PUSH1 0x0a PUSH1 0x0c PUSH1 0x00 CODECOPY PUSH1 0x0a PUSH1 0x00 RETURN
//	runtime: PUSH1 0x2a PUSH1 0x00 MSTORE PUSH1 0x20 PUSH1 0x00 RETURN
var answer42 = []byte{
	0x60, 0x0a, 0x60, 0x0c, 0x60, 0x00, 0x39, 0x60, 0x0a, 0x60, 0x00, 0xf3,
	0x60, 0x2a, 0x60, 0x00, 0x52, 0x60, 0x20, 0x60, 0x00, 0xf3,
}

func TestNew_FundsAccountsAndReportsDevChain(t *testing.T) {
	sim := simchain.New(t, simchain.WithAccounts(3))
	ctx := context.Background()
	rpc := sim.RPC()

	if got := len(sim.Accounts); got != 3 {
		t.Fatalf("accounts = %d, want 3", got)
	}
	id, err := rpc.ChainID(ctx)
	if err != nil || id != simchain.DevChainID {
		t.Fatalf("ChainID = %d, %v; want %d", id, err, simchain.DevChainID)
	}
	for _, a := range sim.Accounts {
		bal, err := rpc.BalanceAt(ctx, a.Address, nil)
		if err != nil {
			t.Fatal(err)
		}
		if bal.Cmp(simchain.DefaultBalance) != 0 {
			t.Fatalf("balance of %s = %s, want %s", a.Address.Hex(), bal, simchain.DefaultBalance)
		}
		// The same seed rebuilds the same signer in a consumer's test.
		if chaintest.NewFakeKeystore(a.Seed).Address() != a.Address {
			t.Fatalf("seed %q does not rebuild the account signer", a.Seed)
		}
	}
}

func TestSendValue_MinesAndReadsBackReceipt(t *testing.T) {
	sim := simchain.New(t)
	ctx := context.Background()
	rpc := sim.RPC()
	from, to := sim.Accounts[0], sim.Accounts[1]

	tx, err := sim.SendValue(ctx, from, to.Address, big.NewInt(1_000))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rpc.TransactionReceipt(ctx, tx.Hash()); !errors.Is(err, ethereum.NotFound) {
		t.Fatalf("receipt before mining: err = %v, want NotFound", err)
	}
	pending, err := rpc.PendingNonceAt(ctx, from.Address)
	if err != nil || pending != 1 {
		t.Fatalf("pending nonce = %d, %v; want 1", pending, err)
	}

	sim.Commit()

	receipt, err := rpc.TransactionReceipt(ctx, tx.Hash())
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful || receipt.BlockNumber.Uint64() != 1 {
		t.Fatalf("receipt = status %d block %s", receipt.Status, receipt.BlockNumber)
	}
	got, _, err := rpc.TransactionByHash(ctx, tx.Hash())
	if err != nil || got.Hash() != tx.Hash() {
		t.Fatalf("TransactionByHash: %v", err)
	}
	bal, _ := rpc.BalanceAt(ctx, to.Address, nil)
	want := new(big.Int).Add(simchain.DefaultBalance, big.NewInt(1_000))
	if bal.Cmp(want) != 0 {
		t.Fatalf("recipient balance = %s, want %s", bal, want)
	}
	head, err := rpc.HeaderByNumber(ctx, nil)
	if err != nil || head.Number.Uint64() != 1 {
		t.Fatalf("head = %v, %v", head, err)
	}
	block, err := rpc.BlockByNumber(ctx, big.NewInt(1))
	if err != nil || len(block.Transactions()) != 1 {
		t.Fatalf("block 1: %v", err)
	}
}

func TestDeploy_ContractRoundTrip(t *testing.T) {
	sim := simchain.New(t)
	ctx := context.Background()
	rpc := sim.RPC()

	addr, receipt, err := sim.Deploy(ctx, sim.Accounts[0], answer42, "")
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ContractAddress != addr || (addr == chain.Address{}) {
		t.Fatalf("contract address = %s", addr.Hex())
	}
	code, err := rpc.CodeAt(ctx, addr, nil)
	if err != nil || len(code) != 10 {
		t.Fatalf("CodeAt = %x, %v; want the 10-byte runtime", code, err)
	}
	out, err := rpc.CallContract(ctx, ethereum.CallMsg{To: &addr}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := new(big.Int).SetBytes(out); got.Int64() != 42 {
		t.Fatalf("CallContract = %s, want 42", got)
	}
	out, err = rpc.PendingCallContract(ctx, ethereum.CallMsg{To: &addr})
	if err != nil || new(big.Int).SetBytes(out).Int64() != 42 {
		t.Fatalf("PendingCallContract = %x, %v", out, err)
	}
	gas, err := rpc.EstimateGas(ctx, ethereum.CallMsg{From: sim.Accounts[0].Address, To: &addr})
	if err != nil || gas == 0 {
		t.Fatalf("EstimateGas = %d, %v", gas, err)
	}
	price, err := rpc.SuggestGasPrice(ctx)
	if err != nil || price.Sign() <= 0 {
		t.Fatalf("SuggestGasPrice = %v, %v", price, err)
	}
	if _, err := rpc.SuggestGasTipCap(ctx); err != nil {
		t.Fatal(err)
	}
	logs, err := rpc.FilterLogs(ctx, ethereum.FilterQuery{Addresses: []chain.Address{addr}})
	if err != nil || len(logs) != 0 {
		t.Fatalf("FilterLogs = %v, %v", logs, err)
	}
}

func TestMineUntil_StopsOnPredicateAndContext(t *testing.T) {
	sim := simchain.New(t)
	ctx := context.Background()
	rpc := sim.RPC()

	sim.MineUntil(ctx, 100, func() bool {
		n, _ := rpc.BlockNumber(ctx)
		return n >= 5
	})
	if n, _ := rpc.BlockNumber(ctx); n != 5 {
		t.Fatalf("block number = %d, want 5", n)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	sim.MineUntil(cancelled, 100, func() bool { return false })
	if n, _ := rpc.BlockNumber(ctx); n != 5 {
		t.Fatalf("cancelled MineUntil mined: block number = %d", n)
	}
	sim.Mine(2)
	if n, _ := rpc.BlockNumber(ctx); n != 7 {
		t.Fatalf("block number after Mine(2) = %d, want 7", n)
	}
}

func TestWrapper_FailsFirstNThenPassesThroughAndCounts(t *testing.T) {
	sim := simchain.New(t)
	ctx := context.Background()
	boom := errors.New("endpoint down")
	w := simchain.Wrap(sim.RPC()).FailFirst(2, boom)

	for i := 0; i < 2; i++ {
		if _, err := w.ChainID(ctx); !errors.Is(err, boom) {
			t.Fatalf("call %d: err = %v, want injected", i, err)
		}
	}
	if id, err := w.ChainID(ctx); err != nil || id != simchain.DevChainID {
		t.Fatalf("third call: %d, %v", id, err)
	}
	if got := w.Calls("ChainID"); got != 3 {
		t.Fatalf("Calls(ChainID) = %d, want 3", got)
	}
	if w.Failed() != 0 {
		t.Fatalf("Failed() = %d, want 0", w.Failed())
	}
}

func TestWrapper_FailAlwaysOnlyNamedMethodsUntilHealed(t *testing.T) {
	sim := simchain.New(t)
	ctx := context.Background()
	boom := errors.New("send refused")
	w := simchain.Wrap(sim.RPC()).Only("SendTransaction").FailAlways(boom)

	if _, err := w.ChainID(ctx); err != nil {
		t.Fatalf("unrelated method failed: %v", err)
	}
	tx, err := sim.NewDynamicFeeTx(ctx, sim.Accounts[0], &sim.Accounts[1].Address, big.NewInt(1), 21_000, nil)
	if err != nil {
		t.Fatal(err)
	}
	signed, _ := sim.SignTx(sim.Accounts[0], tx)
	if err := w.SendTransaction(ctx, signed); !errors.Is(err, boom) {
		t.Fatalf("SendTransaction err = %v, want injected", err)
	}
	w.Heal()
	if err := w.SendTransaction(ctx, signed); err != nil {
		t.Fatalf("after Heal: %v", err)
	}
	sim.Commit()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := w.TransactionReceipt(ctx, signed.Hash()); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("receipt never appeared")
		}
	}
	if w.Calls("SendTransaction") != 2 {
		t.Fatalf("Calls(SendTransaction) = %d, want 2", w.Calls("SendTransaction"))
	}
}
