package grpc

import (
	"context"
	"math/big"

	"github.com/ethereum/go-ethereum/common"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	protocolv1 "github.com/Cloud-SPE/livepeer-network-modules/proto-contracts/livepeer/protocol/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/types"
)

func (a *adapter) GetConfig(ctx context.Context, _ *protocolv1.Empty) (*protocolv1.OperationalConfig, error) {
	cfg, err := a.srv.GetConfig(ctx, struct{}{})
	if err != nil {
		return nil, errorToStatus(err)
	}
	return opConfigToProto(cfg), nil
}

func (a *adapter) SetConfig(ctx context.Context, req *protocolv1.OperationalConfig) (*protocolv1.OperationalConfig, error) {
	cfg, err := opConfigFromProto(req)
	if err != nil {
		return nil, errorToStatus(err)
	}
	stored, err := a.srv.SetConfig(ctx, cfg)
	if err != nil {
		return nil, errorToStatus(err)
	}
	return opConfigToProto(stored), nil
}

func (a *adapter) SetTranscoder(ctx context.Context, req *protocolv1.SetTranscoderRequest) (*protocolv1.TxIntentRef, error) {
	ref, err := a.srv.SetTranscoder(ctx, SetTranscoderRequest{
		RewardCut: req.GetRewardCut(),
		FeeCut:    req.GetFeeCut(),
	})
	if err != nil {
		return nil, errorToStatus(err)
	}
	return pbTxIntentRefFrom(ref), nil
}

func (a *adapter) ForceTransferBond(ctx context.Context, _ *protocolv1.Empty) (*protocolv1.ForceOutcome, error) {
	out, err := a.srv.ForceTransferBond(ctx, struct{}{})
	if err != nil {
		return nil, errorToStatus(err)
	}
	return pbForceOutcomeFrom(out), nil
}

func (a *adapter) ForceWithdrawFees(ctx context.Context, _ *protocolv1.Empty) (*protocolv1.ForceOutcome, error) {
	out, err := a.srv.ForceWithdrawFees(ctx, struct{}{})
	if err != nil {
		return nil, errorToStatus(err)
	}
	return pbForceOutcomeFrom(out), nil
}

func (a *adapter) CastVote(ctx context.Context, req *protocolv1.CastVoteRequest) (*protocolv1.TxIntentRef, error) {
	ref, err := a.srv.CastVote(ctx, CastVoteRequest{
		ProposalID: req.GetProposalId(),
		Support:    req.GetSupport(),
		Reason:     req.GetReason(),
	})
	if err != nil {
		return nil, errorToStatus(err)
	}
	return pbTxIntentRefFrom(ref), nil
}

func (a *adapter) GetTreasuryProposal(ctx context.Context, req *protocolv1.GetTreasuryProposalRequest) (*protocolv1.TreasuryProposal, error) {
	p, err := a.srv.GetTreasuryProposal(ctx, req.GetProposalId())
	if err != nil {
		return nil, errorToStatus(err)
	}
	out := &protocolv1.TreasuryProposal{
		State:    p.State,
		Deadline: p.Deadline,
		Snapshot: p.Snapshot,
		HasVoted: p.HasVoted,
	}
	if p.VotingPower != nil {
		out.VotingPower = p.VotingPower.Bytes()
	}
	return out, nil
}

// opConfigToProto converts the daemon's operational config to the wire
// shape (decimal-string amounts, hex addresses).
func opConfigToProto(c types.OperationalConfig) *protocolv1.OperationalConfig {
	c.Normalize()
	out := &protocolv1.OperationalConfig{
		RoundInitEnabled:      c.RoundInitEnabled,
		RewardEnabled:         c.RewardEnabled,
		RewardBeforeTransfer:  c.RewardBeforeTransfer,
		TransferBondEnabled:   c.TransferBond.Enabled,
		TransferBondMinRetain: types.WeiToDecimal(c.TransferBond.MinRetainWei, types.EthDecimals),
		WithdrawFeesEnabled:   c.WithdrawFees.Enabled,
		WithdrawFeesThreshold: types.WeiToDecimal(c.WithdrawFees.ThresholdWei, types.EthDecimals),
	}
	if c.TransferBond.Receiver != (chain.Address{}) {
		out.TransferBondReceiver = c.TransferBond.Receiver.Hex()
	}
	if c.WithdrawFees.Receiver != (chain.Address{}) {
		out.WithdrawFeesReceiver = c.WithdrawFees.Receiver.Hex()
	}
	return out
}

// opConfigFromProto converts a wire operational config into the daemon's
// struct, parsing decimal amounts to wei and hex addresses to chain.Address.
func opConfigFromProto(p *protocolv1.OperationalConfig) (types.OperationalConfig, error) {
	cfg := types.OperationalConfig{
		RoundInitEnabled:     p.GetRoundInitEnabled(),
		RewardEnabled:        p.GetRewardEnabled(),
		RewardBeforeTransfer: p.GetRewardBeforeTransfer(),
	}
	cfg.TransferBond.Enabled = p.GetTransferBondEnabled()
	cfg.WithdrawFees.Enabled = p.GetWithdrawFeesEnabled()

	if r := p.GetTransferBondReceiver(); r != "" {
		cfg.TransferBond.Receiver = common.HexToAddress(r)
	}
	if r := p.GetWithdrawFeesReceiver(); r != "" {
		cfg.WithdrawFees.Receiver = common.HexToAddress(r)
	}

	retain, err := parseDecimalOrZero(p.GetTransferBondMinRetain())
	if err != nil {
		return types.OperationalConfig{}, err
	}
	cfg.TransferBond.MinRetainWei = retain

	threshold, err := parseDecimalOrZero(p.GetWithdrawFeesThreshold())
	if err != nil {
		return types.OperationalConfig{}, err
	}
	cfg.WithdrawFees.ThresholdWei = threshold
	return cfg, nil
}

func parseDecimalOrZero(s string) (*big.Int, error) {
	if s == "" {
		return new(big.Int), nil
	}
	return types.DecimalToWei(s, types.EthDecimals)
}
