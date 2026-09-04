package roundsmanager

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	chaintesting "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"
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

func TestLastInitializedRound(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	var seen []byte
	r.CallContractFunc = func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		seen = msg.Data
		out := make([]byte, 32)
		new(big.Int).SetUint64(4242).FillBytes(out)
		return out, nil
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FA01"))
	got, err := b.LastInitializedRound(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != 4242 {
		t.Fatalf("LastInitializedRound = %d; want 4242", got)
	}
	if string(seen) != string(selectorLastInitializedRound) {
		t.Fatalf("calldata = %x; want the lastInitializedRound selector", seen)
	}
}

func TestLastInitializedRoundErrors(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	r.CallContractFunc = func(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error) {
		return nil, errors.New("rpc down")
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FA01"))
	if _, err := b.LastInitializedRound(context.Background()); err == nil {
		t.Fatal("expected rpc error")
	}
	r.CallContractFunc = func(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error) {
		return []byte{1, 2, 3}, nil
	}
	if _, err := b.LastInitializedRound(context.Background()); err == nil {
		t.Fatal("expected short-return error")
	}
}

func TestBlockHashForRound(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	var seen []byte
	want := common.HexToHash("0x00000000000000000000000000000000000000000000000000000000deadbeef")
	r.CallContractFunc = func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		seen = msg.Data
		return want.Bytes(), nil
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FA01"))
	got, err := b.BlockHashForRound(context.Background(), 0x0102)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("BlockHashForRound = %s; want %s", got.Hex(), want.Hex())
	}
	if len(seen) != 36 || string(seen[:4]) != string(selectorBlockHashForRound) {
		t.Fatalf("calldata = %x; want selector + one uint256 slot", seen)
	}
	if seen[34] != 0x01 || seen[35] != 0x02 {
		t.Fatalf("round argument encoded as %x; want big-endian 0x0102 in the low bytes", seen[4:])
	}
	for _, b := range seen[4:34] {
		if b != 0 {
			t.Fatalf("round argument has non-zero high bytes: %x", seen[4:])
		}
	}
}

func TestBlockHashForRoundErrors(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	r.CallContractFunc = func(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error) {
		return nil, errors.New("rpc down")
	}
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FA01"))
	if _, err := b.BlockHashForRound(context.Background(), 1); err == nil {
		t.Fatal("expected rpc error")
	}
	r.CallContractFunc = func(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error) {
		return make([]byte, 8), nil
	}
	if _, err := b.BlockHashForRound(context.Background(), 1); err == nil {
		t.Fatal("expected short-return error")
	}
}
