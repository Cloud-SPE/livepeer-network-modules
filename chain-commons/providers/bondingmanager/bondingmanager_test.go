package bondingmanager

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	chaintesting "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
)

func TestNewValidates(t *testing.T) {
	if _, err := New(nil, common.HexToAddress("0x1")); err == nil {
		t.Fatal("expected error on nil rpc")
	}
	r := chaintesting.NewFakeRPC()
	if _, err := New(r, chain.Address{}); err == nil {
		t.Fatal("expected error on zero addr")
	}
	addr := common.HexToAddress("0x000000000000000000000000000000000000FB01")
	b, err := New(r, addr)
	if err != nil {
		t.Fatal(err)
	}
	if b.Address() != addr {
		t.Fatalf("Address = %s, want %s", b.Address(), addr)
	}
}

func TestGetTranscoder(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	calls := 0
	r.CallContractFunc = func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		calls++
		// First call: getTranscoder — return a tuple of 10 slots.
		// Second call: isActiveTranscoder — return true.
		if calls == 1 {
			out := make([]byte, 32*10)
			// slot 0: lastRewardRound = 100
			new(big.Int).SetUint64(100).FillBytes(out[0:32])
			// slot 4: activationRound = 50
			new(big.Int).SetUint64(50).FillBytes(out[4*32 : 5*32])
			// slot 5: deactivationRound = 0 (no deactivation)
			return out, nil
		}
		return EncodeBoolSlot(true), nil
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FB01"))
	tinfo, err := b.GetTranscoder(context.Background(), common.HexToAddress("0x00000000000000000000000000000000000000A1"))
	if err != nil {
		t.Fatal(err)
	}
	if tinfo.LastRewardRound != 100 {
		t.Fatalf("LastRewardRound = %d; want 100", tinfo.LastRewardRound)
	}
	if tinfo.ActivationRound != 50 {
		t.Fatalf("ActivationRound = %d; want 50", tinfo.ActivationRound)
	}
	if tinfo.DeactivationRound != 0 {
		t.Fatalf("DeactivationRound = %d; want 0", tinfo.DeactivationRound)
	}
	if !tinfo.Active {
		t.Fatal("Active = false; want true")
	}
}

func TestGetTranscoderError(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	r.CallContractFunc = func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		return nil, errors.New("boom")
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FB01"))
	if _, err := b.GetTranscoder(context.Background(), common.HexToAddress("0x00000000000000000000000000000000000000A1")); err == nil {
		t.Fatal("expected error")
	}
}

func TestGetTranscoderShort(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	r.CallContractFunc = func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		return []byte{0x01}, nil
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FB01"))
	if _, err := b.GetTranscoder(context.Background(), common.HexToAddress("0x00000000000000000000000000000000000000A1")); err == nil {
		t.Fatal("expected error on short return")
	}
}

func TestIsActiveTranscoderFalse(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	r.CallContractFunc = func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		return EncodeBoolSlot(false), nil
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FB01"))
	got, err := b.IsActiveTranscoder(context.Background(), common.HexToAddress("0x00000000000000000000000000000000000000A1"))
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("IsActiveTranscoder = true; want false")
	}
}

func TestIsActiveTranscoderShort(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	r.CallContractFunc = func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		return nil, nil
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FB01"))
	if _, err := b.IsActiveTranscoder(context.Background(), common.HexToAddress("0x00000000000000000000000000000000000000A1")); err == nil {
		t.Fatal("expected error on short return")
	}
}

func TestPoolWalk(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	first := common.HexToAddress("0x00000000000000000000000000000000000000AA")
	second := common.HexToAddress("0x00000000000000000000000000000000000000BB")
	r.CallContractFunc = func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		// Look at calldata to decide what to return.
		if len(msg.Data) >= 4 && bytes.Equal(msg.Data[:4], selectorGetFirstInPool) {
			return EncodeAddressSlot(first), nil
		}
		if len(msg.Data) >= 4 && bytes.Equal(msg.Data[:4], selectorGetNextInPool) {
			// Inspect arg.
			if len(msg.Data) >= 4+32 {
				var a chain.Address
				copy(a[:], msg.Data[4+12:4+32])
				if a == first {
					return EncodeAddressSlot(second), nil
				}
			}
			// Tail: return zero.
			return EncodeAddressSlot(chain.Address{}), nil
		}
		return nil, errors.New("unexpected call")
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FB01"))
	got, err := b.GetFirstTranscoderInPool(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != first {
		t.Fatalf("first = %s; want %s", got.Hex(), first.Hex())
	}
	next, err := b.GetNextTranscoderInPool(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	if next != second {
		t.Fatalf("next(first) = %s; want %s", next.Hex(), second.Hex())
	}
	tail, err := b.GetNextTranscoderInPool(context.Background(), second)
	if err != nil {
		t.Fatal(err)
	}
	if tail != (chain.Address{}) {
		t.Fatalf("next(second) = %s; want zero", tail.Hex())
	}
}

func TestPoolWalkErrors(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	r.CallContractFunc = func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		return nil, errors.New("rpc down")
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FB01"))
	if _, err := b.GetFirstTranscoderInPool(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if _, err := b.GetNextTranscoderInPool(context.Background(), common.HexToAddress("0xAA")); err == nil {
		t.Fatal("expected error")
	}
}

func TestPoolWalkShort(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	r.CallContractFunc = func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		return []byte{0x01}, nil
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FB01"))
	if _, err := b.GetFirstTranscoderInPool(context.Background()); err == nil {
		t.Fatal("expected error on short")
	}
	if _, err := b.GetNextTranscoderInPool(context.Background(), common.HexToAddress("0xAA")); err == nil {
		t.Fatal("expected error on short")
	}
}

func TestEncodeHelpers(t *testing.T) {
	a := common.HexToAddress("0x00000000000000000000000000000000000000C0")
	if got := decodeAddress(EncodeAddressSlot(a), 0); got != a {
		t.Fatalf("EncodeAddressSlot round-trip: %s", got.Hex())
	}
	if got := decodeUint64(EncodeUintSlot(123456), 0); got != 123456 {
		t.Fatalf("EncodeUintSlot round-trip: %d", got)
	}
	if got := EncodeBoolSlot(true); got[31] != 1 {
		t.Fatalf("EncodeBoolSlot true last byte = %d; want 1", got[31])
	}
	if got := EncodeBoolSlot(false); got[31] != 0 {
		t.Fatalf("EncodeBoolSlot false last byte = %d; want 0", got[31])
	}
}

func TestDecodeShort(t *testing.T) {
	if got := decodeUint64(nil, 0); got != 0 {
		t.Fatalf("decodeUint64 nil = %d; want 0", got)
	}
	if got := decodeAddress(nil, 0); got != (chain.Address{}) {
		t.Fatalf("decodeAddress nil = %s; want zero", got.Hex())
	}
}

func TestTranscoderInfoIsActiveAtRound(t *testing.T) {
	tests := []struct {
		name   string
		t      TranscoderInfo
		round  chain.RoundNumber
		expect bool
	}{
		{"inactive flag", TranscoderInfo{Active: false, ActivationRound: 1}, 5, false},
		{"before activation", TranscoderInfo{Active: true, ActivationRound: 10}, 5, false},
		{"after deactivation", TranscoderInfo{Active: true, ActivationRound: 1, DeactivationRound: 5}, 5, false},
		{"in window", TranscoderInfo{Active: true, ActivationRound: 1, DeactivationRound: 100}, 50, true},
		{"no deactivation set", TranscoderInfo{Active: true, ActivationRound: 1}, 1000, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.t.IsActiveAtRound(tt.round); got != tt.expect {
				t.Fatalf("IsActiveAtRound(%d) = %v; want %v", tt.round, got, tt.expect)
			}
		})
	}
}
