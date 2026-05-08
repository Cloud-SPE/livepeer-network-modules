package chaintesting

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/gasoracle"
)

func TestFakeGasOracle_SuggestDefaults(t *testing.T) {
	g := NewFakeGasOracle()
	e, err := g.Suggest(context.Background())
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if e.BaseFee.Cmp(big.NewInt(1_000_000_000)) != 0 {
		t.Errorf("BaseFee = %s, want 1 gwei", e.BaseFee)
	}
}

func TestFakeGasOracle_SetEstimate(t *testing.T) {
	g := NewFakeGasOracle()
	want := gasoracle.Estimate{
		BaseFee: big.NewInt(2_000_000_000),
		TipCap:  big.NewInt(3_000_000_000),
		FeeCap:  big.NewInt(7_000_000_000),
		Source:  "rpc",
	}
	g.SetEstimate(want)
	got, _ := g.Suggest(context.Background())
	if got.BaseFee.Cmp(want.BaseFee) != 0 {
		t.Errorf("BaseFee = %s, want %s", got.BaseFee, want.BaseFee)
	}
}

func TestFakeGasOracle_SuggestCount(t *testing.T) {
	g := NewFakeGasOracle()
	_, _ = g.Suggest(context.Background())
	_, _ = g.Suggest(context.Background())
	if got := g.SuggestCount(); got != 2 {
		t.Errorf("SuggestCount = %d, want 2", got)
	}
}

func TestFakeGasOracle_FailNextSuggest(t *testing.T) {
	g := NewFakeGasOracle()
	want := errors.New("oracle down")
	g.FailNextSuggest(want)
	if _, err := g.Suggest(context.Background()); err != want {
		t.Errorf("Suggest = %v, want %v", err, want)
	}
	// Recovers on next call.
	if _, err := g.Suggest(context.Background()); err != nil {
		t.Errorf("Second Suggest = %v, want nil", err)
	}
}

func TestFakeGasOracle_SuggestTipCap(t *testing.T) {
	g := NewFakeGasOracle()
	tc, err := g.SuggestTipCap(context.Background())
	if err != nil {
		t.Fatalf("SuggestTipCap: %v", err)
	}
	if tc.Cmp(big.NewInt(1_000_000_000)) != 0 {
		t.Errorf("TipCap = %s", tc)
	}
}

func TestFakeGasOracle_FailNextTipCap(t *testing.T) {
	g := NewFakeGasOracle()
	want := errors.New("tipcap down")
	g.FailNextTipCap(want)
	if _, err := g.SuggestTipCap(context.Background()); err != want {
		t.Errorf("SuggestTipCap = %v, want %v", err, want)
	}
}

func TestFakeGasOracle_ReturnsCopy(t *testing.T) {
	g := NewFakeGasOracle()
	e, _ := g.Suggest(context.Background())
	e.BaseFee.SetUint64(99)
	e2, _ := g.Suggest(context.Background())
	if e2.BaseFee.Uint64() == 99 {
		t.Errorf("Suggest should return a fresh copy of Wei pointers")
	}
}
