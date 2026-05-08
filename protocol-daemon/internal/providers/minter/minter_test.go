package minter

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"

	chaintesting "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"
)

func TestNewValidates(t *testing.T) {
	if _, err := New(nil, common.HexToAddress("0x1")); err == nil {
		t.Fatal("expected error on nil rpc")
	}
	r := chaintesting.NewFakeRPC()
	addr := common.HexToAddress("0x000000000000000000000000000000000000FC01")
	b, err := New(r, addr)
	if err != nil {
		t.Fatal(err)
	}
	if b.Address() != addr {
		t.Fatalf("Address = %s, want %s", b.Address(), addr)
	}
}

func TestNewAcceptsZeroAddress(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	if _, err := New(r, common.Address{}); err != nil {
		t.Fatalf("zero address should be accepted: %v", err)
	}
}
