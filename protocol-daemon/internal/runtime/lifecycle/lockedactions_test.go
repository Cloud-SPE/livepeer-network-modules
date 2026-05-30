package lifecycle

import (
	"context"
	"errors"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/protocol-daemon/internal/service/bondingadmin"
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
