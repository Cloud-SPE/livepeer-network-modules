// Package treasury provides ABI bindings to Livepeer's Treasury, an
// OpenZeppelin Governor contract (registered as "LivepeerGovernor"). It
// builds the castVote / castVoteWithReason calldata the voting flow submits
// and the read methods the console surfaces before a vote (state, hasVoted,
// deadline, snapshot, voting power).
//
// This package is the boundary for go-ethereum imports for governor calls.
package treasury

import (
	"context"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/rpc"
)

// VoteSupport mirrors OpenZeppelin GovernorCountingSimple's support enum.
type VoteSupport uint8

const (
	// VoteAgainst is a vote against the proposal.
	VoteAgainst VoteSupport = 0
	// VoteFor is a vote for the proposal.
	VoteFor VoteSupport = 1
	// VoteAbstain is an abstention.
	VoteAbstain VoteSupport = 2
)

// Valid reports whether the support value is one of the three choices.
func (s VoteSupport) Valid() bool { return s <= VoteAbstain }

// ProposalState mirrors OpenZeppelin IGovernor.ProposalState.
type ProposalState uint8

const (
	StatePending ProposalState = iota
	StateActive
	StateCanceled
	StateDefeated
	StateSucceeded
	StateQueued
	StateExpired
	StateExecuted
)

// String renders the proposal state for display.
func (s ProposalState) String() string {
	switch s {
	case StatePending:
		return "Pending"
	case StateActive:
		return "Active"
	case StateCanceled:
		return "Canceled"
	case StateDefeated:
		return "Defeated"
	case StateSucceeded:
		return "Succeeded"
	case StateQueued:
		return "Queued"
	case StateExpired:
		return "Expired"
	case StateExecuted:
		return "Executed"
	default:
		return fmt.Sprintf("Unknown(%d)", uint8(s))
	}
}

var (
	selectorCastVote           = crypto.Keccak256([]byte("castVote(uint256,uint8)"))[:4]
	selectorCastVoteWithReason = crypto.Keccak256([]byte("castVoteWithReason(uint256,uint8,string)"))[:4]
	selectorState              = crypto.Keccak256([]byte("state(uint256)"))[:4]
	selectorHasVoted           = crypto.Keccak256([]byte("hasVoted(uint256,address)"))[:4]
	selectorProposalDeadline   = crypto.Keccak256([]byte("proposalDeadline(uint256)"))[:4]
	selectorProposalSnapshot   = crypto.Keccak256([]byte("proposalSnapshot(uint256)"))[:4]
	selectorGetVotes           = crypto.Keccak256([]byte("getVotes(address,uint256)"))[:4]
)

// Bindings is the governor surface for protocol-daemon.
type Bindings struct {
	rpc  rpc.RPC
	addr chain.Address
}

// New constructs Bindings for the governor at addr.
func New(r rpc.RPC, addr chain.Address) (*Bindings, error) {
	if r == nil {
		return nil, fmt.Errorf("treasury: rpc is required")
	}
	if addr == (chain.Address{}) {
		return nil, fmt.Errorf("treasury: addr is required")
	}
	return &Bindings{rpc: r, addr: addr}, nil
}

// Address returns the governor contract address.
func (b *Bindings) Address() chain.Address { return b.addr }

// PackCastVote returns the calldata for castVote(uint256 proposalId,
// uint8 support).
func (b *Bindings) PackCastVote(proposalID *big.Int, support VoteSupport) ([]byte, error) {
	if proposalID == nil || proposalID.Sign() < 0 {
		return nil, fmt.Errorf("castVote: invalid proposalID")
	}
	if !support.Valid() {
		return nil, fmt.Errorf("castVote: invalid support %d", support)
	}
	out := make([]byte, 4+32*2)
	copy(out[0:4], selectorCastVote)
	proposalID.FillBytes(out[4 : 4+32])
	out[4+63] = byte(support)
	return out, nil
}

// PackCastVoteWithReason returns the calldata for
// castVoteWithReason(uint256 proposalId, uint8 support, string reason).
func (b *Bindings) PackCastVoteWithReason(proposalID *big.Int, support VoteSupport, reason string) ([]byte, error) {
	if proposalID == nil || proposalID.Sign() < 0 {
		return nil, fmt.Errorf("castVoteWithReason: invalid proposalID")
	}
	if !support.Valid() {
		return nil, fmt.Errorf("castVoteWithReason: invalid support %d", support)
	}
	// Head: proposalId, support, offset-to-string (= 3 words = 0x60).
	head := make([]byte, 32*3)
	proposalID.FillBytes(head[0:32])
	head[63] = byte(support)
	big.NewInt(0x60).FillBytes(head[64:96])

	tail := encodeString(reason)

	out := make([]byte, 0, 4+len(head)+len(tail))
	out = append(out, selectorCastVoteWithReason...)
	out = append(out, head...)
	out = append(out, tail...)
	return out, nil
}

// State calls governor.state(proposalId).
func (b *Bindings) State(ctx context.Context, proposalID *big.Int) (ProposalState, error) {
	out, err := b.callUintWithUint(ctx, "state", selectorState, proposalID)
	if err != nil {
		return 0, err
	}
	return ProposalState(out.Uint64()), nil
}

// ProposalDeadline calls governor.proposalDeadline(proposalId) — the last
// block (or timepoint) at which votes are accepted.
func (b *Bindings) ProposalDeadline(ctx context.Context, proposalID *big.Int) (*big.Int, error) {
	return b.callUintWithUint(ctx, "proposalDeadline", selectorProposalDeadline, proposalID)
}

// ProposalSnapshot calls governor.proposalSnapshot(proposalId) — the
// timepoint at which voting power is measured.
func (b *Bindings) ProposalSnapshot(ctx context.Context, proposalID *big.Int) (*big.Int, error) {
	return b.callUintWithUint(ctx, "proposalSnapshot", selectorProposalSnapshot, proposalID)
}

// HasVoted calls governor.hasVoted(proposalId, account).
func (b *Bindings) HasVoted(ctx context.Context, proposalID *big.Int, account chain.Address) (bool, error) {
	calldata := make([]byte, 4+32*2)
	copy(calldata[0:4], selectorHasVoted)
	proposalID.FillBytes(calldata[4 : 4+32])
	copy(calldata[4+32+12:4+64], account[:])
	out, err := b.call(ctx, "hasVoted", calldata)
	if err != nil {
		return false, err
	}
	if len(out) < 32 {
		return false, fmt.Errorf("treasury.hasVoted: short return")
	}
	return out[len(out)-1] != 0, nil
}

// GetVotes calls governor.getVotes(account, timepoint) — the account's
// voting power at the given timepoint (use ProposalSnapshot for a proposal).
func (b *Bindings) GetVotes(ctx context.Context, account chain.Address, timepoint *big.Int) (*big.Int, error) {
	calldata := make([]byte, 4+32*2)
	copy(calldata[0:4], selectorGetVotes)
	copy(calldata[4+12:4+32], account[:])
	if timepoint != nil {
		timepoint.FillBytes(calldata[4+32 : 4+64])
	}
	out, err := b.call(ctx, "getVotes", calldata)
	if err != nil {
		return nil, err
	}
	if len(out) < 32 {
		return nil, fmt.Errorf("treasury.getVotes: short return")
	}
	return new(big.Int).SetBytes(out[:32]), nil
}

func (b *Bindings) callUintWithUint(ctx context.Context, name string, selector []byte, arg *big.Int) (*big.Int, error) {
	if arg == nil {
		return nil, fmt.Errorf("treasury.%s: nil arg", name)
	}
	calldata := make([]byte, 4+32)
	copy(calldata[0:4], selector)
	arg.FillBytes(calldata[4 : 4+32])
	out, err := b.call(ctx, name, calldata)
	if err != nil {
		return nil, err
	}
	if len(out) < 32 {
		return nil, fmt.Errorf("treasury.%s: short return", name)
	}
	return new(big.Int).SetBytes(out[:32]), nil
}

func (b *Bindings) call(ctx context.Context, name string, calldata []byte) ([]byte, error) {
	to := b.addr
	out, err := b.rpc.CallContract(ctx, ethereum.CallMsg{To: &to, Data: calldata}, nil)
	if err != nil {
		return nil, fmt.Errorf("treasury.%s: %w", name, err)
	}
	return out, nil
}

// encodeString ABI-encodes a dynamic string: a 32-byte length word
// followed by the bytes right-padded to a 32-byte boundary.
func encodeString(s string) []byte {
	data := []byte(s)
	padded := (len(data) + 31) / 32 * 32
	out := make([]byte, 32+padded)
	big.NewInt(int64(len(data))).FillBytes(out[0:32])
	copy(out[32:], data)
	return out
}
