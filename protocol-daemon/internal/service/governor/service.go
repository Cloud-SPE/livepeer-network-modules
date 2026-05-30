// Package governor implements treasury proposal voting against Livepeer's
// LivepeerGovernor (an OpenZeppelin Governor) contract.
//
// Voting is an operator judgment call, not an automated loop: the service
// exposes CastVote (submit) plus ProposalInfo (the pre-vote safety reads
// the console surfaces — state, deadline, whether the wallet already voted,
// and its voting power at the snapshot). The on-chain contract reverts a
// duplicate vote, so idempotency keys on (proposalID, wallet). Every submit
// is dry-run via eth_call first to catch already-voted / closed-window /
// no-power reverts without spending gas.
package governor

import (
	"context"
	"errors"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/txintent"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/providers/treasury"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/types"
)

// Governor is the subset of the treasury provider binding this service uses.
type Governor interface {
	Address() chain.Address
	PackCastVote(proposalID *big.Int, support treasury.VoteSupport) ([]byte, error)
	PackCastVoteWithReason(proposalID *big.Int, support treasury.VoteSupport, reason string) ([]byte, error)
	State(ctx context.Context, proposalID *big.Int) (treasury.ProposalState, error)
	HasVoted(ctx context.Context, proposalID *big.Int, account chain.Address) (bool, error)
	ProposalDeadline(ctx context.Context, proposalID *big.Int) (*big.Int, error)
	ProposalSnapshot(ctx context.Context, proposalID *big.Int) (*big.Int, error)
	GetVotes(ctx context.Context, account chain.Address, timepoint *big.Int) (*big.Int, error)
}

// TxSubmitter is the subset of txintent.Manager used here.
type TxSubmitter interface {
	Submit(ctx context.Context, p txintent.Params) (txintent.IntentID, error)
}

// Caller issues read-only eth_calls for the pre-submit dry-run.
type Caller interface {
	CallContract(ctx context.Context, call ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
}

// Config wires the service.
type Config struct {
	Governor    Governor
	TxIntent    TxSubmitter
	Caller      Caller
	OrchAddress chain.Address
	GasLimit    uint64
	Logger      logger.Logger
}

// Service performs governor voting.
type Service struct {
	cfg Config
}

// New constructs a Service, validating required dependencies.
func New(cfg Config) (*Service, error) {
	if cfg.Governor == nil {
		return nil, errors.New("governor: Governor is required")
	}
	if cfg.TxIntent == nil {
		return nil, errors.New("governor: TxIntent is required")
	}
	if cfg.Caller == nil {
		return nil, errors.New("governor: Caller is required")
	}
	if cfg.OrchAddress == (chain.Address{}) {
		return nil, errors.New("governor: OrchAddress is required")
	}
	if cfg.GasLimit == 0 {
		return nil, errors.New("governor: GasLimit is required (>0)")
	}
	return &Service{cfg: cfg}, nil
}

// ProposalInfo is the pre-vote snapshot the console shows the operator.
type ProposalInfo struct {
	State       treasury.ProposalState
	Deadline    *big.Int
	Snapshot    *big.Int
	HasVoted    bool
	VotingPower *big.Int
}

// Proposal reads the safety info for a proposal: its state, voting window,
// whether the orchestrator wallet has already voted, and its voting power
// measured at the proposal snapshot.
func (s *Service) Proposal(ctx context.Context, proposalID *big.Int) (ProposalInfo, error) {
	if proposalID == nil {
		return ProposalInfo{}, errors.New("governor: proposalID is required")
	}
	st, err := s.cfg.Governor.State(ctx, proposalID)
	if err != nil {
		return ProposalInfo{}, err
	}
	deadline, err := s.cfg.Governor.ProposalDeadline(ctx, proposalID)
	if err != nil {
		return ProposalInfo{}, err
	}
	snapshot, err := s.cfg.Governor.ProposalSnapshot(ctx, proposalID)
	if err != nil {
		return ProposalInfo{}, err
	}
	voted, err := s.cfg.Governor.HasVoted(ctx, proposalID, s.cfg.OrchAddress)
	if err != nil {
		return ProposalInfo{}, err
	}
	power, err := s.cfg.Governor.GetVotes(ctx, s.cfg.OrchAddress, snapshot)
	if err != nil {
		return ProposalInfo{}, err
	}
	return ProposalInfo{
		State:       st,
		Deadline:    deadline,
		Snapshot:    snapshot,
		HasVoted:    voted,
		VotingPower: power,
	}, nil
}

// CastVote submits a vote on proposalID with the given support. When reason
// is non-empty it uses castVoteWithReason, otherwise castVote. The exact
// calldata is dry-run from the orchestrator address first so an already-
// voted / closed-window / no-power proposal fails before any gas is spent.
func (s *Service) CastVote(ctx context.Context, proposalID *big.Int, support treasury.VoteSupport, reason string) (txintent.IntentID, error) {
	if proposalID == nil || proposalID.Sign() < 0 {
		return txintent.IntentID{}, errors.New("governor: invalid proposalID")
	}
	if !support.Valid() {
		return txintent.IntentID{}, fmt.Errorf("governor: invalid support %d", support)
	}

	var (
		calldata []byte
		err      error
	)
	if reason == "" {
		calldata, err = s.cfg.Governor.PackCastVote(proposalID, support)
	} else {
		calldata, err = s.cfg.Governor.PackCastVoteWithReason(proposalID, support, reason)
	}
	if err != nil {
		return txintent.IntentID{}, err
	}

	to := s.cfg.Governor.Address()
	if _, err := s.cfg.Caller.CallContract(ctx, ethereum.CallMsg{
		From: s.cfg.OrchAddress,
		To:   &to,
		Data: calldata,
	}, nil); err != nil {
		if s.cfg.Logger != nil {
			s.cfg.Logger.Warn("governor dry-run reverted",
				logger.String("err_code", types.ErrCodeGovernorDryRunFailed),
				logger.Err(err))
		}
		return txintent.IntentID{}, fmt.Errorf("%s: %w", types.ErrCodeGovernorDryRunFailed, err)
	}

	id, err := s.cfg.TxIntent.Submit(ctx, txintent.Params{
		Kind:      "CastVote",
		KeyParams: voteKey(proposalID, s.cfg.OrchAddress),
		To:        to,
		CallData:  calldata,
		Value:     new(big.Int),
		GasLimit:  s.cfg.GasLimit,
		Metadata: map[string]string{
			"proposal_id": proposalID.String(),
			"support":     fmt.Sprint(support),
		},
	})
	if err != nil {
		return txintent.IntentID{}, fmt.Errorf("%s: %w", types.ErrCodeGovernorSubmitFailed, err)
	}
	if s.cfg.Logger != nil {
		s.cfg.Logger.Info("governor vote submitted",
			logger.String("proposal_id", proposalID.String()),
			logger.String("intent_id", id.Hex()))
	}
	return id, nil
}

// voteKey is the canonical idempotency key for a (proposal, wallet) vote:
// 32-byte proposalID ++ 20-byte wallet.
func voteKey(proposalID *big.Int, wallet chain.Address) []byte {
	out := make([]byte, 32+20)
	proposalID.FillBytes(out[:32])
	copy(out[32:], wallet[:])
	return out
}
