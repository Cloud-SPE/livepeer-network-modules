package bondingmanager

import (
	"bytes"
	"context"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	chaintesting "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"
)

// routeBySelector returns a CallContractFunc that dispatches on the 4-byte
// selector prefix of the calldata to a preconfigured response.
func routeBySelector(responses map[string][]byte) func(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error) {
	return func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		if len(msg.Data) < 4 {
			return nil, nil
		}
		sel := hex.EncodeToString(msg.Data[:4])
		if resp, ok := responses[sel]; ok {
			return resp, nil
		}
		return nil, nil
	}
}

func selHex(sel []byte) string { return hex.EncodeToString(sel) }

func word(v *big.Int) []byte {
	out := make([]byte, 32)
	v.FillBytes(out)
	return out
}

func TestPackTranscoder(t *testing.T) {
	out, err := (&Bindings{}).PackTranscoder(500_000, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4+64 {
		t.Fatalf("want 68 bytes, got %d", len(out))
	}
	wantSel := crypto.Keccak256([]byte("transcoder(uint256,uint256)"))[:4]
	if !bytes.Equal(out[:4], wantSel) {
		t.Fatal("selector mismatch")
	}
	if new(big.Int).SetBytes(out[4:36]).Uint64() != 500_000 {
		t.Error("rewardCut word mismatch")
	}
	if new(big.Int).SetBytes(out[36:68]).Uint64() != 1_000_000 {
		t.Error("feeShare word mismatch")
	}
}

func TestPackTransferBond(t *testing.T) {
	recipient := chain.Address{0xAA}
	amount := big.NewInt(12345)
	out, err := (&Bindings{}).PackTransferBond(recipient, amount, chain.Address{}, chain.Address{}, chain.Address{}, chain.Address{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4+32*6 {
		t.Fatalf("want %d bytes, got %d", 4+32*6, len(out))
	}
	wantSel := crypto.Keccak256([]byte("transferBond(address,uint256,address,address,address,address)"))[:4]
	if !bytes.Equal(out[:4], wantSel) {
		t.Fatal("selector mismatch")
	}
	if !bytes.Equal(out[4+12:4+32], recipient[:]) {
		t.Error("recipient word mismatch")
	}
	if new(big.Int).SetBytes(out[4+32:4+64]).Cmp(amount) != 0 {
		t.Error("amount word mismatch")
	}
}

func TestPackTransferBondRejectsNonPositive(t *testing.T) {
	if _, err := (&Bindings{}).PackTransferBond(chain.Address{0x1}, big.NewInt(0), chain.Address{}, chain.Address{}, chain.Address{}, chain.Address{}); err == nil {
		t.Fatal("expected error for zero amount")
	}
}

func TestPackWithdrawFees(t *testing.T) {
	recipient := chain.Address{0xBB}
	amount := big.NewInt(777)
	out, err := (&Bindings{}).PackWithdrawFees(recipient, amount)
	if err != nil {
		t.Fatal(err)
	}
	wantSel := crypto.Keccak256([]byte("withdrawFees(address,uint256)"))[:4]
	if !bytes.Equal(out[:4], wantSel) {
		t.Fatal("selector mismatch")
	}
	if !bytes.Equal(out[4+12:4+32], recipient[:]) {
		t.Error("recipient mismatch")
	}
	if new(big.Int).SetBytes(out[4+32:4+64]).Cmp(amount) != 0 {
		t.Error("amount mismatch")
	}
}

func TestPackWithdrawFeesRejectsNonPositive(t *testing.T) {
	if _, err := (&Bindings{}).PackWithdrawFees(chain.Address{0x1}, big.NewInt(-1)); err == nil {
		t.Fatal("expected error for negative amount")
	}
}

func TestPendingStakeAndFees(t *testing.T) {
	rpc := chaintesting.NewFakeRPC()
	stake := big.NewInt(1_000_000_000_000_000_000)
	fees := big.NewInt(30_000_000_000_000_000)
	rpc.CallContractFunc = routeBySelector(map[string][]byte{
		selHex(selectorPendingStake): word(stake),
		selHex(selectorPendingFees):  word(fees),
	})

	b, err := New(rpc, chain.Address{0x99})
	if err != nil {
		t.Fatal(err)
	}
	gotStake, err := b.PendingStake(context.Background(), chain.Address{0x10}, 42)
	if err != nil {
		t.Fatal(err)
	}
	if gotStake.Cmp(stake) != 0 {
		t.Errorf("pendingStake = %s, want %s", gotStake, stake)
	}
	gotFees, err := b.PendingFees(context.Background(), chain.Address{0x10}, 42)
	if err != nil {
		t.Fatal(err)
	}
	if gotFees.Cmp(fees) != 0 {
		t.Errorf("pendingFees = %s, want %s", gotFees, fees)
	}
}

func TestGetDelegator(t *testing.T) {
	bonded := big.NewInt(5_000)
	fees := big.NewInt(7)
	delegate := chain.Address{0xCC}

	out := make([]byte, 32*7)
	bonded.FillBytes(out[0:32])
	fees.FillBytes(out[32:64])
	copy(out[64+12:96], delegate[:])

	rpc := chaintesting.NewFakeRPC()
	rpc.CallContractFunc = routeBySelector(map[string][]byte{
		selHex(selectorGetDelegator): out,
	})

	b, err := New(rpc, chain.Address{0x99})
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.GetDelegator(context.Background(), chain.Address{0x10})
	if err != nil {
		t.Fatal(err)
	}
	if got.BondedAmount.Cmp(bonded) != 0 {
		t.Errorf("bonded = %s, want %s", got.BondedAmount, bonded)
	}
	if got.Fees.Cmp(fees) != 0 {
		t.Errorf("fees = %s, want %s", got.Fees, fees)
	}
	if got.DelegateAddress != delegate {
		t.Errorf("delegate = %s, want %s", got.DelegateAddress.Hex(), delegate.Hex())
	}
}
