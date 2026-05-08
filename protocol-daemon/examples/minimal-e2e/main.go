// minimal-e2e is a runnable end-to-end demo against chain-commons.testing
// fakes (FakeRPC + FakeKeystore + FakeReceipts).
//
// The demo:
//  1. Stands up an in-process FakeRPC populated with a fake Controller
//     pointing at synthetic RoundsManager + BondingManager addresses.
//  2. Wires a FakeKeystore; the derived address is the orchestrator.
//  3. Wires the txintent.Manager + DefaultProcessor with a FakeReceipts
//     that auto-confirms every submitted tx.
//  4. Programs FakeRPC's CallContract handler so currentRoundInitialized
//     starts false (round-init service submits) and BondingManager.
//     getTranscoder returns the orch as Active with LastRewardRound < r.
//  5. Drives one chain.Round{Number: 100} event; both InitializeRound
//     and RewardWithHint TxIntents reach confirmed.
//
// Verifies that protocol-daemon's services-on-chain-commons stack is
// end-to-end coherent against fakes — same path operators take in
// production, just with the fakes swapped for real providers.
package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"math/big"
	"os"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	chaincfg "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/controller"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/gasoracle"
	cmetrics "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/receipts"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/txintent"
	chaintesting "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/providers/bondingmanager"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/providers/roundsmanager"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/repo/poolhints"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/reward"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/roundinit"
)

const (
	rmAddrHex = "0x000000000000000000000000000000000000FA01"
	bmAddrHex = "0x000000000000000000000000000000000000FB01"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := runE2E(ctx); err != nil {
		log.Fatalf("minimal-e2e failed: %v", err)
	}
	fmt.Fprintln(os.Stderr, "minimal-e2e: PASS")
}

func runE2E(ctx context.Context) error {
	rmAddr := common.HexToAddress(rmAddrHex)
	bmAddr := common.HexToAddress(bmAddrHex)

	ks := chaintesting.NewFakeKeystore("minimal-e2e-seed")
	orch := ks.Address()
	fmt.Printf("orchestrator address: %s\n", orch.Hex())

	// FakeRPC: program CallContract to answer the calls our services make.
	rpcFake := chaintesting.NewFakeRPC()
	rpcFake.DefaultChainID = 42161
	rpcFake.DefaultBalance = new(big.Int).SetUint64(1e18)

	// Selectors we'll match against.
	selRoundInit := crypto.Keccak256([]byte("currentRoundInitialized()"))[:4]
	selGetTranscoder := crypto.Keccak256([]byte("getTranscoder(address)"))[:4]
	selIsActive := crypto.Keccak256([]byte("isActiveTranscoder(address)"))[:4]
	selFirst := crypto.Keccak256([]byte("getFirstTranscoderInPool()"))[:4]
	selNext := crypto.Keccak256([]byte("getNextTranscoderInPool(address)"))[:4]

	rpcFake.CallContractFunc = func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		if len(msg.Data) < 4 {
			return nil, fmt.Errorf("call too short")
		}
		sel := msg.Data[:4]
		switch {
		case bytes.Equal(sel, selRoundInit):
			// not initialized → true after first call (we're not tracking
			// state; just always-false to make round-init submit).
			return bondingmanager.EncodeBoolSlot(false), nil
		case bytes.Equal(sel, selGetTranscoder):
			out := make([]byte, 32*10)
			// slot 0: lastRewardRound = 99 (less than current round 100)
			new(big.Int).SetUint64(99).FillBytes(out[0:32])
			// slot 4: activationRound = 1
			new(big.Int).SetUint64(1).FillBytes(out[4*32 : 5*32])
			// slot 5: deactivationRound = 0
			return out, nil
		case bytes.Equal(sel, selIsActive):
			return bondingmanager.EncodeBoolSlot(true), nil
		case bytes.Equal(sel, selFirst):
			return bondingmanager.EncodeAddressSlot(orch), nil
		case bytes.Equal(sel, selNext):
			return bondingmanager.EncodeAddressSlot(chain.Address{}), nil
		default:
			return nil, fmt.Errorf("unhandled call selector: %x", sel)
		}
	}

	rpcFake.PendingNonceAtFunc = func(_ context.Context, _ chain.Address) (uint64, error) {
		return 0, nil
	}
	rpcFake.SendTransactionFunc = func(_ context.Context, _ *ethtypes.Transaction) error {
		return nil
	}

	// FakeReceipts auto-confirms every submitted tx with a synthesized
	// reward event.
	rec := chaintesting.NewFakeReceipts()
	rec.SimulateInstant = true
	// We pre-set a generic confirmed receipt for any hash; the actual
	// processor calls Wait with the hash returned by our SignTx (which
	// FakeKeystore signs deterministically).
	// Best-effort: precompute and program — but since the tx hash depends
	// on the gas/nonce/calldata combo, we instead override the txintent
	// processor's Receipts via a custom impl that always returns confirmed.
	rcAll := alwaysConfirmedReceipts{}

	// FakeController returns our synthetic addresses.
	ctrl := chaintesting.NewFakeController(controller.Addresses{
		RoundsManager:  rmAddr,
		BondingManager: bmAddr,
	}, nil)

	gas := constGasOracle{}

	// Store: in-memory.
	st := store.Memory()

	// TxIntent Manager + Processor — the real ones, just wired against fakes.
	policy := chaincfg.Default()
	processor, err := txintent.NewDefaultProcessor(txintent.ProcessorConfig{
		Policy:             policy.TxIntent,
		ChainID:            42161,
		ReorgConfirmations: 1,
		GasLimit:           1_000_000,
		RPC:                rpcFake,
		Keystore:           ks,
		Gas:                gas,
		Receipts:           rcAll,
		Clock:              clock.System(),
		Metrics:            cmetrics.NoOp(),
	})
	if err != nil {
		return fmt.Errorf("processor: %w", err)
	}
	txm, err := txintent.New(policy.TxIntent, st, clock.System(), nil, cmetrics.NoOp(), processor)
	if err != nil {
		return fmt.Errorf("manager: %w", err)
	}
	if err := txm.Resume(ctx); err != nil {
		return fmt.Errorf("resume: %w", err)
	}

	// Build round-init service.
	rmBindings, err := roundsmanager.New(rpcFake, rmAddr)
	if err != nil {
		return err
	}
	roundInitSvc, err := roundinit.New(roundinit.Config{
		RoundsManager: rmBindings,
		TxIntent:      txm,
		GasLimit:      1_000_000,
	})
	if err != nil {
		return err
	}

	// Build reward service.
	bmBindings, err := bondingmanager.New(rpcFake, bmAddr)
	if err != nil {
		return err
	}
	cache, err := poolhints.New(st)
	if err != nil {
		return err
	}
	rewardSvc, err := reward.New(reward.Config{
		BondingManager: bmBindings,
		TxIntent:       txm,
		Cache:          cache,
		OrchAddress:    orch,
		GasLimit:       1_000_000,
	})
	if err != nil {
		return err
	}
	_ = ctrl // controller is wired into preflight in the daemon; unused here

	// Drive one Round event.
	round := chain.Round{Number: 100}

	// 1. round-init: should submit and reach confirmed.
	roundInitRes, err := roundInitSvc.TryInitialize(ctx, round)
	if err != nil {
		return fmt.Errorf("round-init: %w", err)
	}
	if roundInitRes.Skip != nil {
		return fmt.Errorf("round-init skipped (%s) — submit didn't happen", roundInitRes.Skip.Reason)
	}
	fmt.Printf("round-init intent: %s\n", roundInitRes.IntentID.Hex())

	// 2. reward: should walk pool, find orch alone, submit, reach confirmed.
	rewardRes, err := rewardSvc.TryReward(ctx, round)
	if err != nil {
		return fmt.Errorf("reward: %w", err)
	}
	if rewardRes.Skip != nil {
		return fmt.Errorf("reward skipped (%s) — submit didn't happen", rewardRes.Skip.Reason)
	}
	fmt.Printf("reward intent: %s\n", rewardRes.IntentID.Hex())

	// Wait for both intents to reach confirmed.
	for _, id := range []txintent.IntentID{roundInitRes.IntentID, rewardRes.IntentID} {
		t, err := txm.Wait(ctx, id)
		if err != nil {
			return fmt.Errorf("wait %s: %w", id.Hex(), err)
		}
		fmt.Printf("intent %s: %s\n", id.Hex(), t.Status.String())
		if t.Status != txintent.StatusConfirmed {
			return fmt.Errorf("intent %s did not reach confirmed: %s", id.Hex(), t.Status)
		}
	}

	return nil
}

// constGasOracle returns a fixed estimate.
type constGasOracle struct{}

func (constGasOracle) Suggest(_ context.Context) (gasoracle.Estimate, error) {
	return gasoracle.Estimate{
		BaseFee: big.NewInt(1_000_000_000),
		TipCap:  big.NewInt(1_000_000_000),
		FeeCap:  big.NewInt(3_000_000_000),
		Source:  "demo",
	}, nil
}
func (constGasOracle) SuggestTipCap(_ context.Context) (chain.Wei, error) {
	return big.NewInt(1_000_000_000), nil
}

// alwaysConfirmedReceipts always returns Confirmed=true on any txhash.
type alwaysConfirmedReceipts struct{}

func (alwaysConfirmedReceipts) WaitConfirmed(_ context.Context, txHash chain.TxHash, _ uint64) (*receipts.Receipt, error) {
	return &receipts.Receipt{
		TxHash:    txHash,
		Status:    1,
		Confirmed: true,
	}, nil
}
