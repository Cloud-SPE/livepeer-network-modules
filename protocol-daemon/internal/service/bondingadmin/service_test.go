package bondingadmin

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/txintent"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/providers/bondingmanager"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/types"
)

// --- fakes ---

type fakeBM struct {
	pendingStake *big.Int
	pendingFees  *big.Int
	lastReward   chain.RoundNumber
	packErr      error
	hintErr      error
	oldHints     bondingmanager.TranscoderPoolHints
	newHints     bondingmanager.TranscoderPoolHints
	packedHints  [4]chain.Address
}

func (f *fakeBM) Address() chain.Address { return chain.Address{0xBE} }
func (f *fakeBM) PackTranscoder(_, _ uint64) ([]byte, error) {
	return []byte{0x01, 0x02, 0x03, 0x04}, f.packErr
}
func (f *fakeBM) PackTransferBond(_ chain.Address, _ *big.Int, oldPrev, oldNext, newPrev, newNext chain.Address) ([]byte, error) {
	f.packedHints = [4]chain.Address{oldPrev, oldNext, newPrev, newNext}
	return []byte{0x05, 0x06, 0x07, 0x08}, f.packErr
}
func (f *fakeBM) PackWithdrawFees(_ chain.Address, _ *big.Int) ([]byte, error) {
	return []byte{0x09, 0x0a, 0x0b, 0x0c}, f.packErr
}
func (f *fakeBM) PendingStake(_ context.Context, _ chain.Address, _ chain.RoundNumber) (*big.Int, error) {
	return f.pendingStake, nil
}
func (f *fakeBM) PendingFees(_ context.Context, _ chain.Address, _ chain.RoundNumber) (*big.Int, error) {
	return f.pendingFees, nil
}
func (f *fakeBM) GetTranscoder(_ context.Context, _ chain.Address) (bondingmanager.TranscoderInfo, error) {
	return bondingmanager.TranscoderInfo{LastRewardRound: f.lastReward}, nil
}
func (f *fakeBM) TransferBondHints(_ context.Context, _ chain.Address, _ *big.Int) (bondingmanager.TranscoderPoolHints, bondingmanager.TranscoderPoolHints, error) {
	if f.hintErr != nil {
		return bondingmanager.TranscoderPoolHints{}, bondingmanager.TranscoderPoolHints{}, f.hintErr
	}
	return f.oldHints, f.newHints, nil
}

type fakeRM struct {
	initialized bool
	locked      bool
}

func (f *fakeRM) CurrentRoundInitialized(_ context.Context) (bool, error) { return f.initialized, nil }
func (f *fakeRM) CurrentRoundLocked(_ context.Context) (bool, error)      { return f.locked, nil }

type fakeTx struct {
	submitted    int
	lastKind     string
	lastGasLimit uint64
	err          error
}

func (f *fakeTx) Submit(_ context.Context, p txintent.Params) (txintent.IntentID, error) {
	if f.err != nil {
		return txintent.IntentID{}, f.err
	}
	f.submitted++
	f.lastKind = p.Kind
	f.lastGasLimit = p.GasLimit
	return txintent.IntentID{0x01}, nil
}

// fakeEstimatingCaller is a Caller that also implements the optional
// gasEstimator capability, so submitGuarded sizes gas from the estimate.
type fakeEstimatingCaller struct {
	fakeCaller
	estimate    uint64
	estimateErr error
}

func (f *fakeEstimatingCaller) EstimateGas(_ context.Context, _ ethereum.CallMsg) (uint64, error) {
	if f.estimateErr != nil {
		return 0, f.estimateErr
	}
	return f.estimate, nil
}

type fakeCaller struct {
	revert bool
	calls  int
}

func (f *fakeCaller) CallContract(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
	f.calls++
	if f.revert {
		return nil, errors.New("execution reverted")
	}
	return nil, nil
}

type fakeConfig struct{ cfg types.OperationalConfig }

func (f fakeConfig) Get() types.OperationalConfig { return f.cfg }

func newService(t *testing.T, bm BondingManager, rm RoundsManager, tx TxSubmitter, caller Caller, cfg ConfigSource) *Service {
	t.Helper()
	s, err := New(Config{
		BondingManager: bm, RoundsManager: rm, TxIntent: tx, Caller: caller,
		Config: cfg, OrchAddress: chain.Address{0x11}, GasLimit: 1_000_000,
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func enabledTransfer() types.OperationalConfig {
	c := types.DefaultOperationalConfig()
	c.RewardEnabled = false // disable reward-before-transfer guard by default
	c.RewardBeforeTransfer = false
	c.TransferBond.Enabled = true
	c.TransferBond.Receiver = chain.Address{0xAA}
	c.TransferBond.MinRetainWei = big.NewInt(100)
	return c
}

func enabledWithdraw() types.OperationalConfig {
	c := types.DefaultOperationalConfig()
	c.WithdrawFees.Enabled = true
	c.WithdrawFees.Receiver = chain.Address{0xBB}
	c.WithdrawFees.ThresholdWei = big.NewInt(50)
	return c
}

// --- tests ---

func TestSetTranscoderRequiresInitialized(t *testing.T) {
	tx := &fakeTx{}
	s := newService(t, &fakeBM{}, &fakeRM{initialized: false}, tx, &fakeCaller{}, fakeConfig{types.DefaultOperationalConfig()})
	res, err := s.SetTranscoder(context.Background(), 500_000, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if res.Skip == nil {
		t.Fatal("expected skip when round not initialized")
	}
	if tx.submitted != 0 {
		t.Error("should not submit when not initialized")
	}
}

func TestSetTranscoderSubmits(t *testing.T) {
	tx := &fakeTx{}
	caller := &fakeCaller{}
	s := newService(t, &fakeBM{}, &fakeRM{initialized: true}, tx, caller, fakeConfig{types.DefaultOperationalConfig()})
	res, err := s.SetTranscoder(context.Background(), 500_000, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	if res.Skip != nil {
		t.Fatalf("unexpected skip: %v", res.Skip)
	}
	if tx.submitted != 1 || tx.lastKind != "SetTranscoder" {
		t.Errorf("expected one SetTranscoder submit, got %d kind=%s", tx.submitted, tx.lastKind)
	}
	if caller.calls != 1 {
		t.Error("expected one dry-run call")
	}
}

func TestSetTranscoderRejectsOutOfRange(t *testing.T) {
	s := newService(t, &fakeBM{}, &fakeRM{initialized: true}, &fakeTx{}, &fakeCaller{}, fakeConfig{types.DefaultOperationalConfig()})
	if _, err := s.SetTranscoder(context.Background(), types.PPMDenominator+1, 0); err == nil {
		t.Fatal("expected error for ppm out of range")
	}
}

func TestTransferBondDisabled(t *testing.T) {
	tx := &fakeTx{}
	s := newService(t, &fakeBM{}, &fakeRM{initialized: true, locked: true}, tx, &fakeCaller{}, fakeConfig{types.DefaultOperationalConfig()})
	res, err := s.TransferBond(context.Background(), chain.Round{Number: 5})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skip == nil || res.Skip.Code != SkipCodeDisabled {
		t.Fatalf("expected disabled skip, got %+v", res.Skip)
	}
}

func TestTransferBondRequiresLocked(t *testing.T) {
	tx := &fakeTx{}
	s := newService(t, &fakeBM{pendingStake: big.NewInt(1000)}, &fakeRM{initialized: true, locked: false}, tx, &fakeCaller{}, fakeConfig{enabledTransfer()})
	res, err := s.TransferBond(context.Background(), chain.Round{Number: 5})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skip == nil || res.Skip.Code != SkipCodeRoundNotLocked {
		t.Fatalf("expected round-not-locked skip, got %+v", res.Skip)
	}
	if tx.submitted != 0 {
		t.Error("should not submit when not locked")
	}
}

func TestTransferBondNothingToTransfer(t *testing.T) {
	tx := &fakeTx{}
	// pendingStake == retain -> nothing transferable
	s := newService(t, &fakeBM{pendingStake: big.NewInt(100)}, &fakeRM{initialized: true, locked: true}, tx, &fakeCaller{}, fakeConfig{enabledTransfer()})
	res, err := s.TransferBond(context.Background(), chain.Round{Number: 5})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skip == nil || res.Skip.Code != SkipCodeNothingToTransfer {
		t.Fatalf("expected nothing-to-transfer, got %+v", res.Skip)
	}
}

func TestTransferBondSubmits(t *testing.T) {
	tx := &fakeTx{}
	caller := &fakeCaller{}
	s := newService(t, &fakeBM{pendingStake: big.NewInt(1000)}, &fakeRM{initialized: true, locked: true}, tx, caller, fakeConfig{enabledTransfer()})
	res, err := s.TransferBond(context.Background(), chain.Round{Number: 5})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skip != nil {
		t.Fatalf("unexpected skip: %+v", res.Skip)
	}
	if tx.submitted != 1 || tx.lastKind != "TransferBond" {
		t.Errorf("expected TransferBond submit, got %d kind=%s", tx.submitted, tx.lastKind)
	}
}

func TestTransferBondRewardGuard(t *testing.T) {
	cfg := enabledTransfer()
	cfg.RewardEnabled = true
	cfg.RewardBeforeTransfer = true
	tx := &fakeTx{}
	// lastReward < round => guard trips
	bm := &fakeBM{pendingStake: big.NewInt(1000), lastReward: 4}
	s := newService(t, bm, &fakeRM{initialized: true, locked: true}, tx, &fakeCaller{}, fakeConfig{cfg})
	res, err := s.TransferBond(context.Background(), chain.Round{Number: 5})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skip == nil || res.Skip.Code != SkipCodeRewardNotCalled {
		t.Fatalf("expected reward-not-called skip, got %+v", res.Skip)
	}
	// now lastReward == round => proceeds
	bm.lastReward = 5
	res, err = s.TransferBond(context.Background(), chain.Round{Number: 5})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skip != nil {
		t.Fatalf("unexpected skip after reward confirmed: %+v", res.Skip)
	}
}

func TestWithdrawFeesBelowThreshold(t *testing.T) {
	tx := &fakeTx{}
	s := newService(t, &fakeBM{pendingFees: big.NewInt(10)}, &fakeRM{initialized: true, locked: true}, tx, &fakeCaller{}, fakeConfig{enabledWithdraw()})
	res, err := s.WithdrawFees(context.Background(), chain.Round{Number: 5})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skip == nil || res.Skip.Code != SkipCodeBelowFeeThreshold {
		t.Fatalf("expected below-threshold skip, got %+v", res.Skip)
	}
}

func TestWithdrawFeesSubmits(t *testing.T) {
	tx := &fakeTx{}
	s := newService(t, &fakeBM{pendingFees: big.NewInt(1000)}, &fakeRM{initialized: true, locked: true}, tx, &fakeCaller{}, fakeConfig{enabledWithdraw()})
	res, err := s.WithdrawFees(context.Background(), chain.Round{Number: 5})
	if err != nil {
		t.Fatal(err)
	}
	if res.Skip != nil {
		t.Fatalf("unexpected skip: %+v", res.Skip)
	}
	if tx.submitted != 1 || tx.lastKind != "WithdrawFees" {
		t.Errorf("expected WithdrawFees submit, got %d kind=%s", tx.submitted, tx.lastKind)
	}
}

func TestDryRunRevertAborts(t *testing.T) {
	tx := &fakeTx{}
	caller := &fakeCaller{revert: true}
	s := newService(t, &fakeBM{pendingFees: big.NewInt(1000)}, &fakeRM{initialized: true, locked: true}, tx, caller, fakeConfig{enabledWithdraw()})
	_, err := s.WithdrawFees(context.Background(), chain.Round{Number: 5})
	if err == nil {
		t.Fatal("expected dry-run revert to abort submit")
	}
	if tx.submitted != 0 {
		t.Error("should not submit when dry-run reverts")
	}
}

func TestNewValidates(t *testing.T) {
	_, err := New(Config{})
	if err == nil {
		t.Fatal("expected validation error on empty config")
	}
}

func TestTransferBondPassesComputedHints(t *testing.T) {
	tx := &fakeTx{}
	bm := &fakeBM{
		pendingStake: big.NewInt(1000),
		oldHints:     bondingmanager.TranscoderPoolHints{PosPrev: chain.Address{0xA1}, PosNext: chain.Address{0xA2}},
		newHints:     bondingmanager.TranscoderPoolHints{PosPrev: chain.Address{0xB1}, PosNext: chain.Address{0xB2}},
	}
	s := newService(t, bm, &fakeRM{initialized: true, locked: true}, tx, &fakeCaller{}, fakeConfig{enabledTransfer()})
	if _, err := s.TransferBond(context.Background(), chain.Round{Number: 5}); err != nil {
		t.Fatal(err)
	}
	want := [4]chain.Address{{0xA1}, {0xA2}, {0xB1}, {0xB2}}
	if bm.packedHints != want {
		t.Errorf("packed hints = %v, want %v", bm.packedHints, want)
	}
}

func TestTransferBondFallsBackToZeroHintsOnError(t *testing.T) {
	tx := &fakeTx{}
	bm := &fakeBM{
		pendingStake: big.NewInt(1000),
		hintErr:      errors.New("rpc down"),
		// hints set but should be ignored because hintErr is returned
		oldHints: bondingmanager.TranscoderPoolHints{PosPrev: chain.Address{0xA1}},
	}
	s := newService(t, bm, &fakeRM{initialized: true, locked: true}, tx, &fakeCaller{}, fakeConfig{enabledTransfer()})
	if _, err := s.TransferBond(context.Background(), chain.Round{Number: 5}); err != nil {
		t.Fatal(err)
	}
	if bm.packedHints != ([4]chain.Address{}) {
		t.Errorf("expected zero hints on hint error, got %v", bm.packedHints)
	}
	if tx.submitted != 1 {
		t.Errorf("expected submit despite hint failure, got %d", tx.submitted)
	}
}

func TestGasEstimateRaisesLimitAboveFloor(t *testing.T) {
	tx := &fakeTx{}
	// estimate 2_000_000 -> +50% = 3_000_000, above the 1_000_000 floor.
	caller := &fakeEstimatingCaller{estimate: 2_000_000}
	s := newService(t, &fakeBM{pendingStake: big.NewInt(1000)}, &fakeRM{initialized: true, locked: true}, tx, caller, fakeConfig{enabledTransfer()})
	if _, err := s.TransferBond(context.Background(), chain.Round{Number: 5}); err != nil {
		t.Fatal(err)
	}
	if tx.lastGasLimit != 3_000_000 {
		t.Errorf("gas limit = %d, want 3_000_000", tx.lastGasLimit)
	}
}

func TestGasEstimateFlooredToConfig(t *testing.T) {
	tx := &fakeTx{}
	// estimate 400_000 -> +50% = 600_000, below the 1_000_000 floor.
	caller := &fakeEstimatingCaller{estimate: 400_000}
	s := newService(t, &fakeBM{pendingStake: big.NewInt(1000)}, &fakeRM{initialized: true, locked: true}, tx, caller, fakeConfig{enabledTransfer()})
	if _, err := s.TransferBond(context.Background(), chain.Round{Number: 5}); err != nil {
		t.Fatal(err)
	}
	if tx.lastGasLimit != 1_000_000 {
		t.Errorf("gas limit = %d, want 1_000_000 (config floor)", tx.lastGasLimit)
	}
}

func TestGasEstimateErrorFallsBackToConfig(t *testing.T) {
	tx := &fakeTx{}
	caller := &fakeEstimatingCaller{estimateErr: errors.New("estimate failed")}
	s := newService(t, &fakeBM{pendingStake: big.NewInt(1000)}, &fakeRM{initialized: true, locked: true}, tx, caller, fakeConfig{enabledTransfer()})
	if _, err := s.TransferBond(context.Background(), chain.Round{Number: 5}); err != nil {
		t.Fatal(err)
	}
	if tx.lastGasLimit != 1_000_000 {
		t.Errorf("gas limit = %d, want 1_000_000 (config fallback)", tx.lastGasLimit)
	}
}

func TestGasLimitStaticWhenCallerCannotEstimate(t *testing.T) {
	tx := &fakeTx{}
	// fakeCaller does not implement gasEstimator -> static config gas limit.
	s := newService(t, &fakeBM{pendingStake: big.NewInt(1000)}, &fakeRM{initialized: true, locked: true}, tx, &fakeCaller{}, fakeConfig{enabledTransfer()})
	if _, err := s.TransferBond(context.Background(), chain.Round{Number: 5}); err != nil {
		t.Fatal(err)
	}
	if tx.lastGasLimit != 1_000_000 {
		t.Errorf("gas limit = %d, want 1_000_000", tx.lastGasLimit)
	}
}
