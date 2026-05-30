package grpc

import (
	"context"
	"math/big"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/txintent"
	protocolv1 "github.com/Cloud-SPE/livepeer-network-modules/proto-contracts/livepeer/protocol/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/providers/treasury"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/service/bondingadmin"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/service/governor"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/types"
)

type stubBondingAdmin struct {
	gotRewardCutPPM uint64
	gotFeeSharePPM  uint64
	result          bondingadmin.ActionResult
	err             error
}

func (s *stubBondingAdmin) SetTranscoder(_ context.Context, rewardCutPPM, feeSharePPM uint64) (bondingadmin.ActionResult, error) {
	s.gotRewardCutPPM = rewardCutPPM
	s.gotFeeSharePPM = feeSharePPM
	return s.result, s.err
}
func (s *stubBondingAdmin) TransferBond(_ context.Context, _ chain.Round) (bondingadmin.ActionResult, error) {
	return s.result, s.err
}
func (s *stubBondingAdmin) WithdrawFees(_ context.Context, _ chain.Round) (bondingadmin.ActionResult, error) {
	return s.result, s.err
}

type stubConfigStore struct{ cfg types.OperationalConfig }

func (s *stubConfigStore) Get() types.OperationalConfig { return s.cfg }
func (s *stubConfigStore) Set(c types.OperationalConfig) (types.OperationalConfig, error) {
	c.Normalize()
	if err := c.Validate(); err != nil {
		return types.OperationalConfig{}, err
	}
	s.cfg = c
	return c, nil
}

func TestSetTranscoderFlipsFeeCut(t *testing.T) {
	stub := &stubBondingAdmin{result: bondingadmin.ActionResult{IntentID: txintent.IntentID{0x1}}}
	srv := &Server{bondingAdmin: stub}
	_, err := srv.SetTranscoder(context.Background(), SetTranscoderRequest{
		RewardCut: "10",   // orch keeps 10% of rewards -> 100000 ppm
		FeeCut:    "95.5", // orch keeps 95.5% of fees -> fee_share = 4.5% = 45000 ppm
	})
	if err != nil {
		t.Fatal(err)
	}
	if stub.gotRewardCutPPM != 100_000 {
		t.Errorf("rewardCutPPM = %d, want 100000", stub.gotRewardCutPPM)
	}
	// fee_share = (100 - 95.5)% = 4.5% = 45000 ppm
	if stub.gotFeeSharePPM != 45_000 {
		t.Errorf("feeSharePPM = %d, want 45000 (100%%-95.5%% flip)", stub.gotFeeSharePPM)
	}
}

func TestSetTranscoderRejectsBadPercent(t *testing.T) {
	srv := &Server{bondingAdmin: &stubBondingAdmin{}}
	if _, err := srv.SetTranscoder(context.Background(), SetTranscoderRequest{RewardCut: "abc", FeeCut: "10"}); err == nil {
		t.Fatal("expected error for non-numeric reward_cut")
	}
}

func TestSetTranscoderUnimplementedWithoutService(t *testing.T) {
	srv := &Server{}
	if _, err := srv.SetTranscoder(context.Background(), SetTranscoderRequest{RewardCut: "10", FeeCut: "10"}); err != ErrUnimplemented {
		t.Fatalf("want ErrUnimplemented, got %v", err)
	}
}

func TestGetSetConfigRoundTrip(t *testing.T) {
	store := &stubConfigStore{cfg: types.DefaultOperationalConfig()}
	srv := &Server{cfgStore: store}

	got, err := srv.GetConfig(context.Background(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if !got.RewardEnabled {
		t.Error("default should have reward enabled")
	}

	cfg := types.DefaultOperationalConfig()
	cfg.RoundInitEnabled = true
	stored, err := srv.SetConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.RoundInitEnabled {
		t.Error("round-init enable not persisted")
	}
}

func TestSetConfigRejectsInvalid(t *testing.T) {
	store := &stubConfigStore{cfg: types.DefaultOperationalConfig()}
	srv := &Server{cfgStore: store}
	bad := types.DefaultOperationalConfig()
	bad.TransferBond.Enabled = true // no receiver
	if _, err := srv.SetConfig(context.Background(), bad); err == nil {
		t.Fatal("expected validation error")
	}
}

type stubGovernor struct {
	info governor.ProposalInfo
	id   txintent.IntentID
}

func (s *stubGovernor) CastVote(_ context.Context, _ *big.Int, _ treasury.VoteSupport, _ string) (txintent.IntentID, error) {
	return s.id, nil
}
func (s *stubGovernor) Proposal(_ context.Context, _ *big.Int) (governor.ProposalInfo, error) {
	return s.info, nil
}

func TestCastVoteRejectsBadProposalID(t *testing.T) {
	srv := &Server{governor: &stubGovernor{}}
	if _, err := srv.CastVote(context.Background(), CastVoteRequest{ProposalID: "0xnope", Support: 1}); err == nil {
		t.Fatal("expected error for non-decimal proposal_id")
	}
}

func TestCastVoteRejectsBadSupport(t *testing.T) {
	srv := &Server{governor: &stubGovernor{}}
	if _, err := srv.CastVote(context.Background(), CastVoteRequest{ProposalID: "123", Support: 9}); err == nil {
		t.Fatal("expected error for invalid support")
	}
}

func TestCastVoteSubmits(t *testing.T) {
	srv := &Server{governor: &stubGovernor{id: txintent.IntentID{0xAB}}}
	ref, err := srv.CastVote(context.Background(), CastVoteRequest{ProposalID: "123", Support: 1, Reason: "ok"})
	if err != nil {
		t.Fatal(err)
	}
	if ref.ID != (txintent.IntentID{0xAB}) {
		t.Errorf("unexpected intent id %x", ref.ID)
	}
}

func TestForceTransferBondSubmits(t *testing.T) {
	stub := &stubBondingAdmin{result: bondingadmin.ActionResult{IntentID: txintent.IntentID{0x1}}}
	srv := &Server{bondingAdmin: stub, rc: &stubRoundClockSrc{}}
	out, err := srv.ForceTransferBond(context.Background(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Submitted == nil {
		t.Fatalf("expected submitted, got %+v", out)
	}
}

func TestForceWithdrawFeesSkip(t *testing.T) {
	stub := &stubBondingAdmin{result: bondingadmin.ActionResult{Skip: &bondingadmin.SkipReason{
		Reason: "round not locked", Code: bondingadmin.SkipCodeRoundNotLocked,
	}}}
	srv := &Server{bondingAdmin: stub, rc: &stubRoundClockSrc{}}
	out, err := srv.ForceWithdrawFees(context.Background(), struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Skipped == nil || out.Skipped.Code != SkipCodeRoundNotLocked {
		t.Fatalf("expected round-not-locked skip, got %+v", out)
	}
}

func TestForceTransferBondUnimplemented(t *testing.T) {
	srv := &Server{}
	if _, err := srv.ForceTransferBond(context.Background(), struct{}{}); err != ErrUnimplemented {
		t.Fatalf("want ErrUnimplemented, got %v", err)
	}
}

func TestGetTreasuryProposal(t *testing.T) {
	gov := &stubGovernor{info: governor.ProposalInfo{
		State:       treasury.StateActive,
		Deadline:    big.NewInt(99),
		Snapshot:    big.NewInt(88),
		HasVoted:    true,
		VotingPower: big.NewInt(4242),
	}}
	srv := &Server{governor: gov}
	p, err := srv.GetTreasuryProposal(context.Background(), "123")
	if err != nil {
		t.Fatal(err)
	}
	if p.State != "Active" || p.Deadline != 99 || p.Snapshot != 88 || !p.HasVoted {
		t.Fatalf("unexpected proposal snapshot: %+v", p)
	}
	if p.VotingPower.Int64() != 4242 {
		t.Errorf("voting power = %v", p.VotingPower)
	}
}

func TestGetTreasuryProposalRejectsBadID(t *testing.T) {
	srv := &Server{governor: &stubGovernor{}}
	if _, err := srv.GetTreasuryProposal(context.Background(), "0xzz"); err == nil {
		t.Fatal("expected error for non-decimal id")
	}
}

func TestGetTreasuryProposalUnimplemented(t *testing.T) {
	srv := &Server{}
	if _, err := srv.GetTreasuryProposal(context.Background(), "1"); err != ErrUnimplemented {
		t.Fatalf("want ErrUnimplemented, got %v", err)
	}
}

func TestAdapterConfigRoundTrip(t *testing.T) {
	store := &stubConfigStore{cfg: types.DefaultOperationalConfig()}
	a := newAdapter(&Server{cfgStore: store})

	got, err := a.GetConfig(context.Background(), &protocolv1.Empty{})
	if err != nil {
		t.Fatal(err)
	}
	if !got.GetRewardEnabled() {
		t.Error("default reward should be enabled over the wire")
	}

	got.RoundInitEnabled = true
	got.TransferBondEnabled = true
	got.TransferBondReceiver = "0x000000000000000000000000000000000000dEaD"
	got.TransferBondMinRetain = "1"
	stored, err := a.SetConfig(context.Background(), got)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.GetRoundInitEnabled() || !stored.GetTransferBondEnabled() {
		t.Error("flags not persisted over the wire")
	}
	if stored.GetTransferBondMinRetain() != "1" {
		t.Errorf("min retain round-trip = %q", stored.GetTransferBondMinRetain())
	}
}

func TestAdapterSetTranscoder(t *testing.T) {
	stub := &stubBondingAdmin{result: bondingadmin.ActionResult{IntentID: txintent.IntentID{0x7}}}
	a := newAdapter(&Server{bondingAdmin: stub})
	ref, err := a.SetTranscoder(context.Background(), &protocolv1.SetTranscoderRequest{RewardCut: "10", FeeCut: "95.5"})
	if err != nil {
		t.Fatal(err)
	}
	if len(ref.GetId()) != 32 {
		t.Errorf("expected 32-byte id, got %d", len(ref.GetId()))
	}
}

func TestAdapterForceAndVote(t *testing.T) {
	ba := &stubBondingAdmin{result: bondingadmin.ActionResult{IntentID: txintent.IntentID{0x1}}}
	gov := &stubGovernor{id: txintent.IntentID{0x2}, info: governor.ProposalInfo{
		State: treasury.StateActive, Deadline: big.NewInt(1), Snapshot: big.NewInt(1), VotingPower: big.NewInt(5),
	}}
	a := newAdapter(&Server{bondingAdmin: ba, governor: gov, rc: &stubRoundClockSrc{}})

	if _, err := a.ForceTransferBond(context.Background(), &protocolv1.Empty{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ForceWithdrawFees(context.Background(), &protocolv1.Empty{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CastVote(context.Background(), &protocolv1.CastVoteRequest{ProposalId: "12", Support: 1}); err != nil {
		t.Fatal(err)
	}
	p, err := a.GetTreasuryProposal(context.Background(), &protocolv1.GetTreasuryProposalRequest{ProposalId: "12"})
	if err != nil {
		t.Fatal(err)
	}
	if p.GetState() != "Active" {
		t.Errorf("state = %q", p.GetState())
	}
}
