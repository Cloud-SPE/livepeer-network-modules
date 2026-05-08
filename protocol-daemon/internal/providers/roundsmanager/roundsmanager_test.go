package roundsmanager

import (
	"bytes"
	"testing"

	"github.com/ethereum/go-ethereum/common"

	chaintesting "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"
)

func TestPackInitializeRound(t *testing.T) {
	r := chaintesting.NewFakeRPC()
	b, _ := New(r, common.HexToAddress("0x000000000000000000000000000000000000FA01"))
	data, err := b.PackInitializeRound()
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 4 {
		t.Fatalf("PackInitializeRound returned %d bytes, want 4", len(data))
	}
	if !bytes.Equal(data, selectorInitializeRound) {
		t.Fatal("initializeRound selector mismatch")
	}
}
