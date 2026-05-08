package chaintesting

import (
	"context"
	"math/big"
	"testing"
)

func TestFakeGasOracle_SetTipCap(t *testing.T) {
	g := NewFakeGasOracle()
	g.SetTipCap(big.NewInt(99))
	got, err := g.SuggestTipCap(context.Background())
	if err != nil {
		t.Fatalf("SuggestTipCap: %v", err)
	}
	if got.Int64() != 99 {
		t.Errorf("TipCap after SetTipCap = %s, want 99", got)
	}
}

func TestFakeGasOracle_NilTipCap(t *testing.T) {
	g := NewFakeGasOracle()
	g.SetTipCap(nil)
	got, err := g.SuggestTipCap(context.Background())
	if err != nil {
		t.Fatalf("SuggestTipCap: %v", err)
	}
	if got != nil {
		t.Errorf("nil-set TipCap should return nil, got %v", got)
	}
}
