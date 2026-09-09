package main

import (
	"math/big"
	"path/filepath"
	"testing"
)

func TestWholesaleAccountConservation(t *testing.T) {
	account := wholesaleAccount{
		ChainID: 42161, Denomination: "wei", Credited: "1000",
		Reserved: "200", Debited: "300", Available: "500",
	}
	if err := account.validate(); err != nil {
		t.Fatal(err)
	}
	account.Available = "501"
	if err := account.validate(); err == nil {
		t.Fatal("non-conserving account passed validation")
	}
}

func TestRecoveryCheckpointIsExclusiveThenReplaceable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "recovery.json")
	cp := &wholesaleRecoveryCheckpoint{
		Version: wholesaleRecoveryCheckpointVersion,
		Phase:   "preparing",
		Payer:   "0x0000000000000000000000000000000000000001",
	}
	if err := writeRecoveryCheckpoint(path, cp, true); err != nil {
		t.Fatal(err)
	}
	if err := writeRecoveryCheckpoint(path, cp, true); err == nil {
		t.Fatal("exclusive checkpoint creation overwrote an existing recovery marker")
	}
	cp.Phase = "admitted"
	if err := writeRecoveryCheckpoint(path, cp, false); err != nil {
		t.Fatal(err)
	}
	got, err := readRecoveryCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phase != "admitted" || got.Payer != cp.Payer {
		t.Fatalf("checkpoint = %+v", got)
	}
}

func TestPositiveDifference(t *testing.T) {
	for _, tc := range []struct {
		target, available, want int64
	}{
		{100, 40, 60},
		{100, 100, 0},
		{100, 140, 0},
	} {
		got := positiveDifference(big.NewInt(tc.target), big.NewInt(tc.available))
		if got.Int64() != tc.want {
			t.Fatalf("positiveDifference(%d, %d) = %s; want %d", tc.target, tc.available, got, tc.want)
		}
	}
}
