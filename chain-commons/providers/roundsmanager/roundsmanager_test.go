package roundsmanager

import (
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
	addr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	b, err := New(r, addr)
	if err != nil {
		t.Fatal(err)
	}
	if b.Address() != addr {
		t.Fatalf("Address = %s, want %s", b.Address(), addr)
	}
}

func TestCurrentRoundInitializedTrue(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	r.CallContractFunc = func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		out := make([]byte, 32)
		out[31] = 1
		return out, nil
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FA01"))
	got, err := b.CurrentRoundInitialized(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("CurrentRoundInitialized = false; want true")
	}
}

func TestCurrentRoundInitializedFalse(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	r.CallContractFunc = func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		return make([]byte, 32), nil
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FA01"))
	got, err := b.CurrentRoundInitialized(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("CurrentRoundInitialized = true; want false")
	}
}

func TestCurrentRoundInitializedError(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	r.CallContractFunc = func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		return nil, errors.New("boom")
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FA01"))
	if _, err := b.CurrentRoundInitialized(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestCurrentRoundInitializedShort(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	r.CallContractFunc = func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		return []byte{0x01}, nil
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FA01"))
	if _, err := b.CurrentRoundInitialized(context.Background()); err == nil {
		t.Fatal("expected error on short return")
	}
}

func TestCurrentRound(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	r.CallContractFunc = func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		out := make([]byte, 32)
		// uint256 with low 8 bytes = 12345
		new(big.Int).SetUint64(12345).FillBytes(out)
		return out, nil
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FA01"))
	got, err := b.CurrentRound(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != 12345 {
		t.Fatalf("CurrentRound = %d; want 12345", got)
	}
}

func TestCurrentRoundError(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	r.CallContractFunc = func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		return nil, errors.New("boom")
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FA01"))
	if _, err := b.CurrentRound(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestCurrentRoundShort(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	r.CallContractFunc = func(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		return []byte{0x01, 0x02}, nil
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FA01"))
	if _, err := b.CurrentRound(context.Background()); err == nil {
		t.Fatal("expected error on short return")
	}
}

func TestDecodeUint64(t *testing.T) {
	in := make([]byte, 32)
	new(big.Int).SetUint64(0xDEADBEEF).FillBytes(in)
	if got := decodeUint64(in); got != 0xDEADBEEF {
		t.Fatalf("decodeUint64 = %x; want DEADBEEF", got)
	}
	if got := decodeUint64([]byte{0x01}); got != 0 {
		t.Fatalf("decodeUint64 short = %d; want 0", got)
	}
}
