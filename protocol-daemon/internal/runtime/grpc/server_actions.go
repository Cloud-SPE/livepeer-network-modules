package grpc

import (
	"context"
	"fmt"
	"math/big"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/providers/treasury"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/service/bondingadmin"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/types"
)

// SetTranscoderRequest mirrors proto.SetTranscoderRequest: operator
// percentages meaning "what the orchestrator keeps".
type SetTranscoderRequest struct {
	RewardCut string
	FeeCut    string
}

// CastVoteRequest mirrors proto.CastVoteRequest.
type CastVoteRequest struct {
	ProposalID string
	Support    uint32
	Reason     string
}

// TreasuryProposal mirrors proto.TreasuryProposal.
type TreasuryProposal struct {
	State       string
	Deadline    uint64
	Snapshot    uint64
	HasVoted    bool
	VotingPower *big.Int
}

// GetConfig returns the daemon's current operational config.
func (s *Server) GetConfig(_ context.Context, _ struct{}) (types.OperationalConfig, error) {
	if s.cfgStore == nil {
		return types.OperationalConfig{}, ErrUnimplemented
	}
	return s.cfgStore.Get(), nil
}

// SetConfig validates and persists a new operational config, returning the
// stored (normalized) result.
func (s *Server) SetConfig(_ context.Context, cfg types.OperationalConfig) (types.OperationalConfig, error) {
	if s.cfgStore == nil {
		return types.OperationalConfig{}, ErrUnimplemented
	}
	stored, err := s.cfgStore.Set(cfg)
	if err != nil {
		return types.OperationalConfig{}, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	return stored, nil
}

// SetTranscoder converts the operator's reward-cut / fee-cut percentages to
// ppm (flipping fee_cut into the contract's fee_share) and submits a
// transcoder() tx. A skip (e.g. round not initialized) surfaces as an error
// since this RPC returns only a ref.
func (s *Server) SetTranscoder(ctx context.Context, req SetTranscoderRequest) (TxIntentRef, error) {
	if s.bondingAdmin == nil {
		return TxIntentRef{}, ErrUnimplemented
	}
	rewardCutPPM, err := types.PercentToPPM(req.RewardCut)
	if err != nil {
		return TxIntentRef{}, fmt.Errorf("%w: reward_cut: %v", ErrInvalidArgument, err)
	}
	feeCutPPM, err := types.PercentToPPM(req.FeeCut)
	if err != nil {
		return TxIntentRef{}, fmt.Errorf("%w: fee_cut: %v", ErrInvalidArgument, err)
	}
	// Contract stores fee_share = delegators' share = 100% − fee_cut.
	feeSharePPM := uint64(types.PPMDenominator) - feeCutPPM
	res, err := s.bondingAdmin.SetTranscoder(ctx, rewardCutPPM, feeSharePPM)
	if err != nil {
		return TxIntentRef{}, err
	}
	if res.Skip != nil {
		return TxIntentRef{}, fmt.Errorf("%w: %s", ErrInvalidArgument, res.Skip.Reason)
	}
	return TxIntentRef{ID: res.IntentID}, nil
}

// ForceTransferBond triggers the round-locked transfer-bond handler now.
func (s *Server) ForceTransferBond(ctx context.Context, _ struct{}) (ForceOutcome, error) {
	if s.bondingAdmin == nil {
		return ForceOutcome{}, ErrUnimplemented
	}
	round, err := s.currentRound(ctx)
	if err != nil {
		return ForceOutcome{}, err
	}
	res, err := s.bondingAdmin.TransferBond(ctx, round)
	if err != nil {
		return ForceOutcome{}, err
	}
	return forceOutcomeFromAction(res), nil
}

// ForceWithdrawFees triggers the round-locked withdraw-fees handler now.
func (s *Server) ForceWithdrawFees(ctx context.Context, _ struct{}) (ForceOutcome, error) {
	if s.bondingAdmin == nil {
		return ForceOutcome{}, ErrUnimplemented
	}
	round, err := s.currentRound(ctx)
	if err != nil {
		return ForceOutcome{}, err
	}
	res, err := s.bondingAdmin.WithdrawFees(ctx, round)
	if err != nil {
		return ForceOutcome{}, err
	}
	return forceOutcomeFromAction(res), nil
}

// CastVote votes on a treasury proposal.
func (s *Server) CastVote(ctx context.Context, req CastVoteRequest) (TxIntentRef, error) {
	if s.governor == nil {
		return TxIntentRef{}, ErrUnimplemented
	}
	pid, ok := new(big.Int).SetString(req.ProposalID, 10)
	if !ok {
		return TxIntentRef{}, fmt.Errorf("%w: proposal_id must be a decimal integer", ErrInvalidArgument)
	}
	support := treasury.VoteSupport(req.Support)
	if !support.Valid() {
		return TxIntentRef{}, fmt.Errorf("%w: support must be 0 (Against), 1 (For), or 2 (Abstain)", ErrInvalidArgument)
	}
	id, err := s.governor.CastVote(ctx, pid, support, req.Reason)
	if err != nil {
		return TxIntentRef{}, err
	}
	return TxIntentRef{ID: id}, nil
}

// GetTreasuryProposal returns the pre-vote safety snapshot for a proposal.
func (s *Server) GetTreasuryProposal(ctx context.Context, proposalID string) (TreasuryProposal, error) {
	if s.governor == nil {
		return TreasuryProposal{}, ErrUnimplemented
	}
	pid, ok := new(big.Int).SetString(proposalID, 10)
	if !ok {
		return TreasuryProposal{}, fmt.Errorf("%w: proposal_id must be a decimal integer", ErrInvalidArgument)
	}
	info, err := s.governor.Proposal(ctx, pid)
	if err != nil {
		return TreasuryProposal{}, err
	}
	return TreasuryProposal{
		State:       info.State.String(),
		Deadline:    bigToUint64(info.Deadline),
		Snapshot:    bigToUint64(info.Snapshot),
		HasVoted:    info.HasVoted,
		VotingPower: info.VotingPower,
	}, nil
}

// currentRound resolves the round for the force actions.
func (s *Server) currentRound(ctx context.Context) (chain.Round, error) {
	if s.rc == nil {
		return chain.Round{}, ErrUnimplemented
	}
	return s.rc.Current(ctx)
}

func forceOutcomeFromAction(r bondingadmin.ActionResult) ForceOutcome {
	if r.Skip != nil {
		return ForceOutcome{Skipped: &SkipReason{
			Reason: r.Skip.Reason,
			Code:   SkipCode(r.Skip.Code),
		}}
	}
	return ForceOutcome{Submitted: &TxIntentRef{ID: r.IntentID}}
}

func bigToUint64(v *big.Int) uint64 {
	if v == nil {
		return 0
	}
	return v.Uint64()
}
