package roundsmanager

import (
	"context"
	"encoding/hex"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	chaintesting "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"
)

func word(v uint64) []byte {
	out := make([]byte, 32)
	new(big.Int).SetUint64(v).FillBytes(out)
	return out
}

func routeBySelector(responses map[string][]byte) func(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error) {
	return func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		if len(msg.Data) < 4 {
			return nil, nil
		}
		if resp, ok := responses[hex.EncodeToString(msg.Data[:4])]; ok {
			return resp, nil
		}
		return nil, nil
	}
}

func TestLockReads(t *testing.T) {
	rpc := chaintesting.NewFakeRPC()
	rpc.CallContractFunc = routeBySelector(map[string][]byte{
		hex.EncodeToString(selectorCurrentRoundStartBlock): word(1000),
		hex.EncodeToString(selectorRoundLength):            word(5760),
		hex.EncodeToString(selectorRoundLockAmount):        word(100),
		hex.EncodeToString(selectorCurrentRoundLocked):     word(1),
	})
	b, err := New(rpc, chain.Address{0x99})
	if err != nil {
		t.Fatal(err)
	}

	start, err := b.CurrentRoundStartBlock(context.Background())
	if err != nil || start != 1000 {
		t.Fatalf("start=%d err=%v", start, err)
	}
	length, err := b.RoundLength(context.Background())
	if err != nil || length != 5760 {
		t.Fatalf("length=%d err=%v", length, err)
	}
	lock, err := b.RoundLockAmount(context.Background())
	if err != nil || lock != 100 {
		t.Fatalf("lock=%d err=%v", lock, err)
	}
	locked, err := b.CurrentRoundLocked(context.Background())
	if err != nil || !locked {
		t.Fatalf("locked=%v err=%v", locked, err)
	}

	if got := uint64(start) + uint64(length) - uint64(lock); got != 6660 {
		t.Fatalf("lockBlock = %d, want 6660", got)
	}
}

func TestCurrentRoundLockedFalse(t *testing.T) {
	rpc := chaintesting.NewFakeRPC()
	rpc.CallContractFunc = routeBySelector(map[string][]byte{
		hex.EncodeToString(selectorCurrentRoundLocked): word(0),
	})
	b, _ := New(rpc, chain.Address{0x99})
	locked, err := b.CurrentRoundLocked(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if locked {
		t.Error("expected not locked")
	}
}
