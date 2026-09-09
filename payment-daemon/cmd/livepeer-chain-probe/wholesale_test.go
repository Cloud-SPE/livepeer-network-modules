package main

import (
	"math/big"
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
