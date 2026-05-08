package serviceregistry

import (
	"context"
	"encoding/binary"
	"math/big"
	"testing"

	ethereum "github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"

	chaintesting "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"
)

func TestNewRequiresAddress(t *testing.T) {
	if _, err := New(common.Address{}); err == nil {
		t.Fatal("expected error on zero address")
	}
}

func TestPackSetServiceURI(t *testing.T) {
	b, err := New(common.HexToAddress("0x0000000000000000000000000000000000001234"))
	if err != nil {
		t.Fatal(err)
	}

	got, err := b.PackSetServiceURI("https://orch.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) < 4+64 {
		t.Fatalf("calldata too short: %d", len(got))
	}
	if string(got[:4]) != string(selectorSetServiceURI) {
		t.Fatal("selector mismatch")
	}
	if off := binary.BigEndian.Uint64(got[4+24 : 4+32]); off != 32 {
		t.Fatalf("offset = %d; want 32", off)
	}
	if ln := binary.BigEndian.Uint64(got[4+32+24 : 4+64]); ln != uint64(len("https://orch.example.com")) {
		t.Fatalf("length = %d", ln)
	}
	if tail := string(got[4+64 : 4+64+len("https://orch.example.com")]); tail != "https://orch.example.com" {
		t.Fatalf("uri = %q", tail)
	}
}

func TestPackSetServiceURIRejectsEmpty(t *testing.T) {
	b, err := New(common.HexToAddress("0x0000000000000000000000000000000000001234"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.PackSetServiceURI(""); err == nil {
		t.Fatal("expected error on empty uri")
	}
}

func TestGetServiceURI(t *testing.T) {
	reg := common.HexToAddress("0x0000000000000000000000000000000000001234")
	orch := common.HexToAddress("0x0000000000000000000000000000000000005678")
	rpcFake := chaintesting.NewFakeRPC()
	rpcFake.CallContractFunc = func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		if msg.To == nil || *msg.To != reg {
			t.Fatalf("call target = %v", msg.To)
		}
		if len(msg.Data) != 36 {
			t.Fatalf("calldata len = %d; want 36", len(msg.Data))
		}
		if got := common.BytesToAddress(msg.Data[16:36]); got != orch {
			t.Fatalf("orch = %s; want %s", got.Hex(), orch.Hex())
		}
		return encodeABIString("https://orch.example.com"), nil
	}
	b, err := New(reg, rpcFake)
	if err != nil {
		t.Fatal(err)
	}
	got, err := b.GetServiceURI(context.Background(), orch)
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://orch.example.com" {
		t.Fatalf("uri = %q", got)
	}
}

func TestGetServiceURIRequiresRPC(t *testing.T) {
	b, err := New(common.HexToAddress("0x0000000000000000000000000000000000001234"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.GetServiceURI(context.Background(), common.HexToAddress("0x0000000000000000000000000000000000005678")); err == nil {
		t.Fatal("expected error without rpc")
	}
}

func encodeABIString(v string) []byte {
	paddedLen := ((len(v) + 31) / 32) * 32
	out := make([]byte, 64+paddedLen)
	binary.BigEndian.PutUint64(out[24:32], 32)
	binary.BigEndian.PutUint64(out[56:64], uint64(len(v)))
	copy(out[64:], []byte(v))
	return out
}
