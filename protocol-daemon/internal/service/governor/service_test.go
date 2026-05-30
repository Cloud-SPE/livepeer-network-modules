package governor

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/txintent"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/providers/treasury"
)

type fakeGov struct {
	state    treasury.ProposalState
	deadline *big.Int
	snapshot *big.Int
	hasVoted bool
	votes    *big.Int
}

func (f *fakeGov) Address() chain.Address { return chain.Address{0x60} }
func (f *fakeGov) PackCastVote(_ *big.Int, _ treasury.VoteSupport) ([]byte, error) {
	return []byte{0x01, 0x02, 0x03, 0x04}, nil
}
func (f *fakeGov) PackCastVoteWithReason(_ *big.Int, _ treasury.VoteSupport, _ string) ([]byte, error) {
	return []byte{0x05, 0x06, 0x07, 0x08}, nil
}
func (f *fakeGov) State(_ context.Context, _ *big.Int) (treasury.ProposalState, error) {
	return f.state, nil
}
func (f *fakeGov) HasVoted(_ context.Context, _ *big.Int, _ chain.Address) (bool, error) {
	return f.hasVoted, nil
}
func (f *fakeGov) ProposalDeadline(_ context.Context, _ *big.Int) (*big.Int, error) {
	return f.deadline, nil
}
func (f *fakeGov) ProposalSnapshot(_ context.Context, _ *big.Int) (*big.Int, error) {
	return f.snapshot, nil
}
func (f *fakeGov) GetVotes(_ context.Context, _ chain.Address, _ *big.Int) (*big.Int, error) {
	return f.votes, nil
}

type fakeTx struct {
	submitted int
	lastKind  string
}

func (f *fakeTx) Submit(_ context.Context, p txintent.Params) (txintent.IntentID, error) {
	f.submitted++
	f.lastKind = p.Kind
	return txintent.IntentID{0x01}, nil
}

type fakeCaller struct {
	revert  bool
	lastLen int
}

func (f *fakeCaller) CallContract(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
	f.lastLen = len(msg.Data)
	if f.revert {
		return nil, errors.New("execution reverted: already voted")
	}
	return nil, nil
}

func newService(t *testing.T, gov Governor, tx TxSubmitter, caller Caller) *Service {
	t.Helper()
	s, err := New(Config{Governor: gov, TxIntent: tx, Caller: caller, OrchAddress: chain.Address{0x11}, GasLimit: 1_000_000})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestProposalReads(t *testing.T) {
	gov := &fakeGov{
		state:    treasury.StateActive,
		deadline: big.NewInt(99999),
		snapshot: big.NewInt(88888),
		hasVoted: false,
		votes:    big.NewInt(4242),
	}
	s := newService(t, gov, &fakeTx{}, &fakeCaller{})
	info, err := s.Proposal(context.Background(), big.NewInt(7))
	if err != nil {
		t.Fatal(err)
	}
	if info.State != treasury.StateActive {
		t.Errorf("state = %v", info.State)
	}
	if info.VotingPower.Int64() != 4242 {
		t.Errorf("power = %v", info.VotingPower)
	}
	if info.HasVoted {
		t.Error("hasVoted should be false")
	}
}

func TestCastVoteNoReason(t *testing.T) {
	tx := &fakeTx{}
	s := newService(t, &fakeGov{}, tx, &fakeCaller{})
	id, err := s.CastVote(context.Background(), big.NewInt(7), treasury.VoteFor, "")
	if err != nil {
		t.Fatal(err)
	}
	if id == (txintent.IntentID{}) {
		t.Error("expected non-zero intent id")
	}
	if tx.submitted != 1 || tx.lastKind != "CastVote" {
		t.Errorf("expected CastVote submit, got %d kind=%s", tx.submitted, tx.lastKind)
	}
}

func TestCastVoteWithReasonUsesReasonBuilder(t *testing.T) {
	tx := &fakeTx{}
	caller := &fakeCaller{}
	s := newService(t, &fakeGov{}, tx, caller)
	if _, err := s.CastVote(context.Background(), big.NewInt(7), treasury.VoteAbstain, "because"); err != nil {
		t.Fatal(err)
	}
	// the with-reason fake builder returns a distinct 4-byte payload (0x05..)
	if caller.lastLen != 4 {
		t.Errorf("unexpected calldata len %d", caller.lastLen)
	}
	if tx.submitted != 1 {
		t.Error("expected submit")
	}
}

func TestCastVoteInvalidSupport(t *testing.T) {
	s := newService(t, &fakeGov{}, &fakeTx{}, &fakeCaller{})
	if _, err := s.CastVote(context.Background(), big.NewInt(7), treasury.VoteSupport(9), ""); err == nil {
		t.Fatal("expected error for invalid support")
	}
}

func TestCastVoteDryRunRevertAborts(t *testing.T) {
	tx := &fakeTx{}
	s := newService(t, &fakeGov{}, tx, &fakeCaller{revert: true})
	if _, err := s.CastVote(context.Background(), big.NewInt(7), treasury.VoteFor, ""); err == nil {
		t.Fatal("expected dry-run revert to abort")
	}
	if tx.submitted != 0 {
		t.Error("should not submit when dry-run reverts")
	}
}

func TestNewValidates(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected validation error")
	}
}
