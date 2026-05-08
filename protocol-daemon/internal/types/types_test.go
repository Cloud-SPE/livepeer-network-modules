package types

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestModeValidate(t *testing.T) {
	cases := []struct {
		m       Mode
		wantErr bool
	}{
		{ModeRoundInit, false},
		{ModeReward, false},
		{ModeBoth, false},
		{Mode("bogus"), true},
		{Mode(""), true},
	}
	for _, c := range cases {
		t.Run(string(c.m), func(t *testing.T) {
			err := c.m.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("validate %q: got err=%v, want err=%v", c.m, err, c.wantErr)
			}
		})
	}
}

func TestModeHelpers(t *testing.T) {
	if !ModeRoundInit.HasRoundInit() {
		t.Fatal("ModeRoundInit.HasRoundInit() = false")
	}
	if ModeRoundInit.HasReward() {
		t.Fatal("ModeRoundInit.HasReward() = true")
	}
	if ModeReward.HasRoundInit() {
		t.Fatal("ModeReward.HasRoundInit() = true")
	}
	if !ModeReward.HasReward() {
		t.Fatal("ModeReward.HasReward() = false")
	}
	if !ModeBoth.HasRoundInit() {
		t.Fatal("ModeBoth.HasRoundInit() = false")
	}
	if !ModeBoth.HasReward() {
		t.Fatal("ModeBoth.HasReward() = false")
	}
	if ModeBoth.String() != "both" {
		t.Fatal("ModeBoth.String() != both")
	}
}

func TestPoolHintsIsZero(t *testing.T) {
	if !(PoolHints{}).IsZero() {
		t.Fatal("zero PoolHints should be zero")
	}
	addr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	if (PoolHints{Prev: addr}).IsZero() {
		t.Fatal("PoolHints with Prev set should not be zero")
	}
	if (PoolHints{Next: addr}).IsZero() {
		t.Fatal("PoolHints with Next set should not be zero")
	}
}
