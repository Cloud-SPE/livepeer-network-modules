package treasury

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

func word(v uint64) []byte {
	out := make([]byte, 32)
	new(big.Int).SetUint64(v).FillBytes(out)
	return out
}

func TestPackCastVote(t *testing.T) {
	out, err := (&Bindings{}).PackCastVote(big.NewInt(42), VoteFor)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4+64 {
		t.Fatalf("want 68 bytes, got %d", len(out))
	}
	wantSel := crypto.Keccak256([]byte("castVote(uint256,uint8)"))[:4]
	if !bytes.Equal(out[:4], wantSel) {
		t.Fatal("selector mismatch")
	}
	if new(big.Int).SetBytes(out[4:36]).Int64() != 42 {
		t.Error("proposalID mismatch")
	}
	if out[67] != 1 {
		t.Errorf("support byte = %d, want 1", out[67])
	}
}

func TestPackCastVoteInvalidSupport(t *testing.T) {
	if _, err := (&Bindings{}).PackCastVote(big.NewInt(1), VoteSupport(9)); err == nil {
		t.Fatal("expected error for invalid support")
	}
}

func TestPackCastVoteWithReason(t *testing.T) {
	reason := "looks good"
	out, err := (&Bindings{}).PackCastVoteWithReason(big.NewInt(7), VoteAbstain, reason)
	if err != nil {
		t.Fatal(err)
	}
	wantSel := crypto.Keccak256([]byte("castVoteWithReason(uint256,uint8,string)"))[:4]
	if !bytes.Equal(out[:4], wantSel) {
		t.Fatal("selector mismatch")
	}
	if new(big.Int).SetBytes(out[4:36]).Int64() != 7 {
		t.Error("proposalID mismatch")
	}
	if out[4+63] != 2 {
		t.Errorf("support = %d, want 2", out[4+63])
	}
	if new(big.Int).SetBytes(out[4+64:4+96]).Int64() != 0x60 {
		t.Error("string offset should be 0x60")
	}
	gotLen := new(big.Int).SetBytes(out[4+96 : 4+128]).Int64()
	if gotLen != int64(len(reason)) {
		t.Errorf("reason length = %d, want %d", gotLen, len(reason))
	}
	if string(out[4+128:4+128+len(reason)]) != reason {
		t.Error("reason bytes mismatch")
	}
	if (len(out)-4-96-32)%32 != 0 {
		t.Error("string data not padded to 32 bytes")
	}
}

func TestReads(t *testing.T) {
	rpc := chaintesting.NewFakeRPC()
	rpc.CallContractFunc = routeBySelector(map[string][]byte{
		hex.EncodeToString(selectorState):            word(uint64(StateActive)),
		hex.EncodeToString(selectorProposalDeadline): word(99999),
		hex.EncodeToString(selectorProposalSnapshot): word(88888),
		hex.EncodeToString(selectorGetVotes):         word(4242),
		hex.EncodeToString(selectorHasVoted):         word(1),
	})
	b, err := New(rpc, chain.Address{0x99})
	if err != nil {
		t.Fatal(err)
	}
	pid := big.NewInt(123)

	st, err := b.State(context.Background(), pid)
	if err != nil || st != StateActive {
		t.Fatalf("state=%v err=%v", st, err)
	}
	if st.String() != "Active" {
		t.Errorf("state string = %q", st.String())
	}
	dl, err := b.ProposalDeadline(context.Background(), pid)
	if err != nil || dl.Int64() != 99999 {
		t.Fatalf("deadline=%v err=%v", dl, err)
	}
	snap, err := b.ProposalSnapshot(context.Background(), pid)
	if err != nil || snap.Int64() != 88888 {
		t.Fatalf("snapshot=%v err=%v", snap, err)
	}
	votes, err := b.GetVotes(context.Background(), chain.Address{0x1}, snap)
	if err != nil || votes.Int64() != 4242 {
		t.Fatalf("votes=%v err=%v", votes, err)
	}
	voted, err := b.HasVoted(context.Background(), pid, chain.Address{0x1})
	if err != nil || !voted {
		t.Fatalf("hasVoted=%v err=%v", voted, err)
	}
}

func TestNewValidates(t *testing.T) {
	if _, err := New(nil, chain.Address{0x1}); err == nil {
		t.Error("expected error for nil rpc")
	}
	if _, err := New(chaintesting.NewFakeRPC(), chain.Address{}); err == nil {
		t.Error("expected error for zero addr")
	}
}
