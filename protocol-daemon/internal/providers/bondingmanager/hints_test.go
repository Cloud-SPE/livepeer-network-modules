package bondingmanager

import (
	"context"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	chaintesting "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"
)

// Selectors for the pool-walk reads (mirror chain-commons, which keeps them
// unexported).
var (
	selFirstInPool = crypto.Keccak256([]byte("getFirstTranscoderInPool()"))[:4]
	selNextInPool  = crypto.Keccak256([]byte("getNextTranscoderInPool(address)"))[:4]
)

// poolFake builds a CallContractFunc serving a fixed active set: the linked
// list walk, per-transcoder total stake, and the pool max size. `ordered` is
// the list in its native descending-stake order.
func poolFake(ordered []PoolEntry, maxSize uint64) func(context.Context, ethereum.CallMsg, *big.Int) ([]byte, error) {
	stakeByAddr := map[chain.Address]*big.Int{}
	nextByAddr := map[chain.Address]chain.Address{}
	for i, e := range ordered {
		stakeByAddr[e.Address] = e.Stake
		if i+1 < len(ordered) {
			nextByAddr[e.Address] = ordered[i+1].Address
		} else {
			nextByAddr[e.Address] = chain.Address{}
		}
	}
	argAddr := func(data []byte) chain.Address {
		var a chain.Address
		copy(a[:], data[4+12:4+32])
		return a
	}
	return func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		switch {
		case has(msg.Data, selectorPoolMaxSize):
			return EncodeUintSlot(maxSize), nil
		case has(msg.Data, selFirstInPool):
			if len(ordered) == 0 {
				return EncodeAddressSlot(chain.Address{}), nil
			}
			return EncodeAddressSlot(ordered[0].Address), nil
		case has(msg.Data, selNextInPool):
			return EncodeAddressSlot(nextByAddr[argAddr(msg.Data)]), nil
		case has(msg.Data, selectorTranscoderTotalStake):
			s := stakeByAddr[argAddr(msg.Data)]
			if s == nil {
				s = big.NewInt(0)
			}
			return EncodeUintSlot(s.Uint64()), nil
		}
		return nil, nil
	}
}

func has(data, sel []byte) bool {
	return len(data) >= 4 && string(data[:4]) == string(sel)
}

func addr(b byte) chain.Address { return chain.Address{b} }

func TestTransferBondHints(t *testing.T) {
	// Active set, descending: A=300, O=200(orch), B=180, C=100. Full at 4.
	a, o, bb, c := addr(0xAA), addr(0x11), addr(0xBB), addr(0xCC)
	ordered := []PoolEntry{
		{a, big.NewInt(300)},
		{o, big.NewInt(200)},
		{bb, big.NewInt(180)},
		{c, big.NewInt(100)},
	}
	rpc := chaintesting.NewFakeRPC()
	rpc.CallContractFunc = poolFake(ordered, 4)
	b, err := New(rpc, chain.Address{0x99})
	if err != nil {
		t.Fatal(err)
	}

	// Transfer 50: orch's transient unbond key is 150 (sinks below B=180);
	// the rebond restores it to 200 (its current slot).
	oldH, newH, err := b.TransferBondHints(context.Background(), o, big.NewInt(50))
	if err != nil {
		t.Fatal(err)
	}
	// At stake 150: A(300), B(180), O(150), C(100) -> prev=B, next=C.
	if oldH.PosPrev != bb || oldH.PosNext != c {
		t.Errorf("oldHints = (%s,%s), want (%s,%s)", oldH.PosPrev.Hex(), oldH.PosNext.Hex(), bb.Hex(), c.Hex())
	}
	// At stake 200 (unchanged): A(300), O(200), B(180), C(100) -> prev=A, next=B.
	if newH.PosPrev != a || newH.PosNext != bb {
		t.Errorf("newHints = (%s,%s), want (%s,%s)", newH.PosPrev.Hex(), newH.PosNext.Hex(), a.Hex(), bb.Hex())
	}
}

func TestTransferBondHintsHeadTranscoder(t *testing.T) {
	// Orch is the highest-staked; a small transfer keeps it at the head, so
	// PosPrev stays zero (no predecessor) for both repositions.
	o, bb, c := addr(0x11), addr(0xBB), addr(0xCC)
	ordered := []PoolEntry{
		{o, big.NewInt(500)},
		{bb, big.NewInt(180)},
		{c, big.NewInt(100)},
	}
	rpc := chaintesting.NewFakeRPC()
	rpc.CallContractFunc = poolFake(ordered, 100) // not full
	b, _ := New(rpc, chain.Address{0x99})

	oldH, newH, err := b.TransferBondHints(context.Background(), o, big.NewInt(50))
	if err != nil {
		t.Fatal(err)
	}
	if oldH.PosPrev != (chain.Address{}) || oldH.PosNext != bb {
		t.Errorf("oldHints = (%s,%s), want (zero,%s)", oldH.PosPrev.Hex(), oldH.PosNext.Hex(), bb.Hex())
	}
	if newH.PosPrev != (chain.Address{}) || newH.PosNext != bb {
		t.Errorf("newHints = (%s,%s), want (zero,%s)", newH.PosPrev.Hex(), newH.PosNext.Hex(), bb.Hex())
	}
}

func TestTransferBondHintsOrchNotInPool(t *testing.T) {
	// Orch absent from the active set -> zero hints, no error.
	bb, c := addr(0xBB), addr(0xCC)
	ordered := []PoolEntry{{bb, big.NewInt(180)}, {c, big.NewInt(100)}}
	rpc := chaintesting.NewFakeRPC()
	rpc.CallContractFunc = poolFake(ordered, 100)
	b, _ := New(rpc, chain.Address{0x99})

	oldH, newH, err := b.TransferBondHints(context.Background(), addr(0x11), big.NewInt(50))
	if err != nil {
		t.Fatal(err)
	}
	if oldH != (TranscoderPoolHints{}) || newH != (TranscoderPoolHints{}) {
		t.Errorf("expected zero hints for non-member orch, got old=%+v new=%+v", oldH, newH)
	}
}

func TestSimulatePoolUpdateEvictsOnlyForNewMember(t *testing.T) {
	a, o, bb := addr(0xAA), addr(0x11), addr(0xBB)
	full := []PoolEntry{{a, big.NewInt(300)}, {o, big.NewInt(200)}, {bb, big.NewInt(100)}}

	// Existing member repositioned in a full pool: size unchanged, tail (B)
	// is retained so the member can still have a valid next neighbor.
	h := simulatePoolUpdate(o, big.NewInt(150), full, true)
	if h.PosPrev != a || h.PosNext != bb {
		t.Errorf("member reposition: got (%s,%s), want (%s,%s)", h.PosPrev.Hex(), h.PosNext.Hex(), a.Hex(), bb.Hex())
	}

	// New member joining a full pool evicts the lowest (B); the joiner lands
	// between A and the now-evicted tail, so its next becomes zero.
	newcomer := addr(0xDD)
	pool := []PoolEntry{{a, big.NewInt(300)}, {o, big.NewInt(200)}, {bb, big.NewInt(100)}}
	h = simulatePoolUpdate(newcomer, big.NewInt(150), pool, true)
	if h.PosPrev != o || h.PosNext != (chain.Address{}) {
		t.Errorf("new member: got (%s,%s), want (%s,zero)", h.PosPrev.Hex(), h.PosNext.Hex(), o.Hex())
	}
}

func TestTranscoderPoolDetectsCycle(t *testing.T) {
	// next(B) points back to A -> cycle; walk must error, not spin.
	a, bb := addr(0xAA), addr(0xBB)
	rpc := chaintesting.NewFakeRPC()
	rpc.CallContractFunc = func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		switch {
		case has(msg.Data, selectorPoolMaxSize):
			return EncodeUintSlot(100), nil
		case has(msg.Data, selFirstInPool):
			return EncodeAddressSlot(a), nil
		case has(msg.Data, selNextInPool):
			var arg chain.Address
			copy(arg[:], msg.Data[4+12:4+32])
			if arg == a {
				return EncodeAddressSlot(bb), nil
			}
			return EncodeAddressSlot(a), nil // B -> A, cycle
		case has(msg.Data, selectorTranscoderTotalStake):
			return EncodeUintSlot(100), nil
		}
		return nil, nil
	}
	b, _ := New(rpc, chain.Address{0x99})
	if _, err := b.TranscoderPool(context.Background()); err == nil {
		t.Fatal("expected cycle-detection error, got nil")
	}
}
