package grpc

import (
	"context"
	"math/big"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/txintent"
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
