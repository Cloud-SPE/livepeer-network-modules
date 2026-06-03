package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/service/bondingadmin"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/types"
)

func TestLockTriggerBlock(t *testing.T) {
	r := chain.Round{L1StartBlock: 1000, Length: 5760}
	if got := lockTriggerBlock(r, 100); got != 6660 {
		t.Errorf("lockTriggerBlock = %d, want 6660", got)
	}
	// lockAmount larger than the window → clamp to start.
	if got := lockTriggerBlock(r, 999999); got != 1000 {
		t.Errorf("clamped lockTriggerBlock = %d, want 1000", got)
	}
}

func TestAttemptTerminalClassification(t *testing.T) {
	round := chain.Round{Number: 5}
	cases := []struct {
		name     string
		res      bondingadmin.ActionResult
		err      error
		terminal bool
	}{
		{"submitted", bondingadmin.ActionResult{IntentID: [32]byte{0x1}}, nil, true},
		{"disabled", bondingadmin.ActionResult{Skip: &bondingadmin.SkipReason{Code: bondingadmin.SkipCodeDisabled}}, nil, true},
		{"nothing", bondingadmin.ActionResult{Skip: &bondingadmin.SkipReason{Code: bondingadmin.SkipCodeNothingToTransfer}}, nil, true},
		{"below-threshold", bondingadmin.ActionResult{Skip: &bondingadmin.SkipReason{Code: bondingadmin.SkipCodeBelowFeeThreshold}}, nil, true},
		{"not-locked-retry", bondingadmin.ActionResult{Skip: &bondingadmin.SkipReason{Code: bondingadmin.SkipCodeRoundNotLocked}}, nil, false},
		{"reward-not-called-retry", bondingadmin.ActionResult{Skip: &bondingadmin.SkipReason{Code: bondingadmin.SkipCodeRewardNotCalled}}, nil, false},
		{"error-retry", bondingadmin.ActionResult{}, errors.New("boom"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fn := func(context.Context, chain.Round) (bondingadmin.ActionResult, error) {
				return c.res, c.err
			}
			if got := attempt(context.Background(), c.name, round, fn, nil); got != c.terminal {
				t.Errorf("attempt terminal = %v, want %v", got, c.terminal)
			}
		})
	}
}

func TestPruneFired(t *testing.T) {
	m := map[chain.RoundNumber]bool{1: true, 2: true, 3: true}
	pruneFired(m, 3)
	if len(m) != 1 || !m[3] {
		t.Errorf("pruneFired kept wrong entries: %+v", m)
	}
}

// --- driver test for runLockedActions ---

// chanRoundClock is a roundclock.Clock whose Subscribe channels the test
// drives directly.
type chanRoundClock struct {
	rounds chan chain.Round
	blocks chan chain.BlockNumber
}

func (c *chanRoundClock) SubscribeRounds(_ context.Context) (<-chan chain.Round, error) {
	return c.rounds, nil
}
func (c *chanRoundClock) SubscribeL1Blocks(_ context.Context) (<-chan chain.BlockNumber, error) {
	return c.blocks, nil
}
func (c *chanRoundClock) Current(_ context.Context) (chain.Round, error) {
	return chain.Round{}, nil
}

type fakeLockedActions struct {
	transfer chan chain.RoundNumber
	withdraw chan chain.RoundNumber
}

func (f *fakeLockedActions) TransferBond(_ context.Context, r chain.Round) (bondingadmin.ActionResult, error) {
	f.transfer <- r.Number
	return bondingadmin.ActionResult{IntentID: [32]byte{0x1}}, nil
}
func (f *fakeLockedActions) WithdrawFees(_ context.Context, r chain.Round) (bondingadmin.ActionResult, error) {
	f.withdraw <- r.Number
	return bondingadmin.ActionResult{IntentID: [32]byte{0x2}}, nil
}

type fakeLockReader struct{ amount chain.BlockNumber }

func (f fakeLockReader) RoundLockAmount(_ context.Context) (chain.BlockNumber, error) {
	return f.amount, nil
}

func TestRunLockedActionsFiresAtLockBlock(t *testing.T) {
	rc := &chanRoundClock{
		rounds: make(chan chain.Round, 1),
		blocks: make(chan chain.BlockNumber, 4),
	}
	acts := &fakeLockedActions{
		transfer: make(chan chain.RoundNumber, 1),
		withdraw: make(chan chain.RoundNumber, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		_ = runLockedActions(ctx, rc, acts, fakeLockReader{amount: 100}, nil)
		close(done)
	}()

	// Round: lockBlock = 1000 + 5760 - 100 = 6660.
	rc.rounds <- chain.Round{Number: 7, L1StartBlock: 1000, Length: 5760}
	// A block before the lock window: nothing fires.
	rc.blocks <- 5000
	// A block in the lock window: both actions fire once.
	rc.blocks <- 6660

	if got := <-acts.transfer; got != 7 {
		t.Errorf("transfer fired for round %d, want 7", got)
	}
	if got := <-acts.withdraw; got != 7 {
		t.Errorf("withdraw fired for round %d, want 7", got)
	}

	cancel()
	<-done
}

func TestRunStartsLockedActionRunner(t *testing.T) {
	rc := &chanRoundClock{
		rounds: make(chan chain.Round),
		blocks: make(chan chain.BlockNumber),
	}
	acts := &fakeLockedActions{
		transfer: make(chan chain.RoundNumber, 1),
		withdraw: make(chan chain.RoundNumber, 1),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Mode:          types.ModeReward,
			Reward:        newReward(t),
			RoundClock:    rc,
			LockedActions: acts,
			LockReader:    fakeLockReader{amount: 100},
		})
	}()
	// Let the runner subscribe, then shut down cleanly.
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run err = %v", err)
	}
}
