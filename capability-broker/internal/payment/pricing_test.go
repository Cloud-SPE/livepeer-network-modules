package payment

import (
	"math/big"
	"testing"
)

// TestBillForMatchesTheSpecRule pins offering-axes.md §6.1. These vectors
// are deliberately the same shape as the payee daemon's own billing test:
// the two implementations live in separate modules and must agree.
func TestBillForMatchesTheSpecRule(t *testing.T) {
	price := big.NewInt(100)
	cases := []struct {
		units, perUnits uint64
		want            int64
	}{
		{units: 0, perUnits: 1000, want: 0},
		{units: 1, perUnits: 1000, want: 1},  // 0.1 wei rounds up
		{units: 10, perUnits: 1000, want: 1}, // exactly 1
		{units: 11, perUnits: 1000, want: 2},
		{units: 3, perUnits: 1, want: 300},
		{units: 3, perUnits: 0, want: 300}, // 0 reads as 1
	}
	for _, c := range cases {
		if got := BillFor(price, c.perUnits, c.units); got.Int64() != c.want {
			t.Fatalf("BillFor(100, %d, %d) = %s; want %d", c.perUnits, c.units, got, c.want)
		}
	}
}

// TestCumulativeBillingHasNoDrift: the sum of incremental charges must
// equal one ceiling over the running total, no matter how the work was
// chunked. Charging each increment on its own would return 1000 here.
func TestCumulativeBillingHasNoDrift(t *testing.T) {
	price := big.NewInt(100)
	const perUnits = 1000

	total := new(big.Int)
	var cumulative uint64
	for i := 0; i < 1000; i++ {
		before := BillFor(price, perUnits, cumulative)
		cumulative++
		total.Add(total, new(big.Int).Sub(BillFor(price, perUnits, cumulative), before))
	}
	if want := BillFor(price, perUnits, cumulative); total.Cmp(want) != 0 {
		t.Fatalf("sum of 1000 increments = %s; want one ceiling over the total, %s", total, want)
	}
	if total.Int64() != 100 {
		t.Fatalf("1000 units at 100 wei per 1000 units = %s wei; want 100", total)
	}
}

// TestRunwayUnitsFloors: a partially affordable unit is not runway.
func TestRunwayUnitsFloors(t *testing.T) {
	price := big.NewInt(100)
	if got := RunwayUnits(big.NewInt(1), price, 1000); got != 10 {
		t.Fatalf("1 wei at 100 per 1000 units = %d units; want 10", got)
	}
	if got := RunwayUnits(big.NewInt(99), price, 1); got != 0 {
		t.Fatalf("99 wei at 100 per unit = %d units; want 0", got)
	}
	if got := RunwayUnits(big.NewInt(0), price, 1); got != 0 {
		t.Fatalf("empty balance = %d units; want 0", got)
	}
}
