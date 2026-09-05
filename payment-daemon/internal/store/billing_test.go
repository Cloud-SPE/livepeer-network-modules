package store

import (
	"errors"
	"math/big"
	"testing"
)

// TestBillForCeilings pins the rule LOC and the broker both implement:
// bill(U) = ceil(U * price / per_units).
func TestBillForCeilings(t *testing.T) {
	price := big.NewInt(100)
	cases := []struct {
		units    uint64
		perUnits uint64
		want     int64
	}{
		{units: 0, perUnits: 1000, want: 0},
		{units: 1, perUnits: 1000, want: 1},  // 0.1 wei rounds up
		{units: 10, perUnits: 1000, want: 1}, // exactly 1
		{units: 11, perUnits: 1000, want: 2}, // 1.1 rounds up
		{units: 3, perUnits: 1, want: 300},   // no denominator
		{units: 3, perUnits: 0, want: 300},   // 0 reads as 1
	}
	for _, c := range cases {
		if got := BillFor(price, c.perUnits, c.units); got.Int64() != c.want {
			t.Fatalf("BillFor(100, %d, %d) = %s; want %d", c.perUnits, c.units, got, c.want)
		}
	}
}

// TestDebitsDoNotAccumulateRoundingDrift is the property the cumulative
// rule exists for. A thousand one-unit debits at a denominator of 1000
// must cost what a single thousand-unit debit costs — 100 wei. Rounding
// each debit on its own would charge 1 wei apiece: 1000 wei, ten times
// the real price, and a payer that computes the honest figure would
// disagree with the ledger about every session it ever funds.
func TestDebitsDoNotAccumulateRoundingDrift(t *testing.T) {
	st := openTestStore(t)
	seed := Session{
		WorkID:              "w1",
		Capability:          "c",
		Offering:            "o",
		PricePerWorkUnitWei: "100",
		PerUnits:            1000,
		WorkUnit:            "tokens",
		BalanceWei:          "1000000",
	}
	if _, _, err := st.OpenSession(seed); err != nil {
		t.Fatal(err)
	}
	sender := []byte{0xAB}
	if err := st.SealSender("w1", sender); err != nil {
		t.Fatal(err)
	}

	start := big.NewInt(1000000)
	for i := uint64(1); i <= 1000; i++ {
		if _, err := st.DebitBalance(sender, "w1", 1, i); err != nil {
			t.Fatalf("debit %d: %v", i, err)
		}
	}
	sess, err := st.Get(sender, "w1")
	if err != nil {
		t.Fatal(err)
	}
	spent := new(big.Int).Sub(start, parseDecimalBig(sess.BalanceWei))
	if spent.Int64() != 100 {
		t.Fatalf("1000 units at 100 wei per 1000 units cost %s wei; want 100", spent)
	}
	if sess.DebitedUnits != 1000 {
		t.Fatalf("debited units = %d; want 1000", sess.DebitedUnits)
	}
}

// TestDebitIdempotencyDoesNotAdvanceUnits: a replayed debit_seq must
// leave the cumulative total alone, or the retry the daemon exists to
// absorb would move the bill.
func TestDebitIdempotencyDoesNotAdvanceUnits(t *testing.T) {
	st := openTestStore(t)
	if _, _, err := st.OpenSession(Session{
		WorkID: "w1", Capability: "c", Offering: "o",
		PricePerWorkUnitWei: "10", PerUnits: 1, WorkUnit: "tokens", BalanceWei: "1000",
	}); err != nil {
		t.Fatal(err)
	}
	sender := []byte{0xAB}
	if err := st.SealSender("w1", sender); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := st.DebitBalance(sender, "w1", 5, 1); err != nil {
			t.Fatal(err)
		}
	}
	sess, err := st.Get(sender, "w1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.DebitedUnits != 5 {
		t.Fatalf("debited units = %d after three identical debits; want 5", sess.DebitedUnits)
	}
	if sess.BalanceWei != "950" {
		t.Fatalf("balance = %s; want 950 (one debit of 5 units at 10 wei)", sess.BalanceWei)
	}
}

// TestUnpricedSessionRefusesToBill is the defect the first chain run
// found: GetTicketParams creates the payment session before the broker
// can price it, and treating "nobody set a price" as zero delivered work
// for free while every log line reported success.
func TestUnpricedSessionRefusesToBill(t *testing.T) {
	st := openTestStore(t)
	if _, _, err := st.OpenSession(Session{
		WorkID: "w1", Capability: "c", Offering: "o",
		PricePerWorkUnitWei: PricingUnset, WorkUnit: "", BalanceWei: "1000",
	}); err != nil {
		t.Fatal(err)
	}
	sender := []byte{0xAB}
	if err := st.SealSender("w1", sender); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DebitBalance(sender, "w1", 42, 1); !errors.Is(err, ErrPricingUnset) {
		t.Fatalf("debit on an unpriced session = %v; want ErrPricingUnset", err)
	}
}

// TestOpenSessionPricesOnceThenRefusesToMove: the ticket-params call
// creates the session unpriced, the broker's open fills it in, and no
// later open may move it — re-pricing a live session bills already-funded
// work at a rate the payer never agreed to.
func TestOpenSessionPricesOnceThenRefusesToMove(t *testing.T) {
	st := openTestStore(t)

	// As GetTicketParams leaves it.
	if _, _, err := st.OpenSession(Session{
		WorkID: "w1", PricePerWorkUnitWei: PricingUnset, BalanceWei: "0",
	}); err != nil {
		t.Fatal(err)
	}
	// As the broker prices it.
	sess, already, err := st.OpenSession(Session{
		WorkID: "w1", Capability: "c", Offering: "o",
		PricePerWorkUnitWei: "7", PerUnits: 1000, WorkUnit: "tokens",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !already {
		t.Fatal("expected the ticket-params session to be reused")
	}
	if sess.PricePerWorkUnitWei != "7" || sess.PerUnits != 1000 || sess.WorkUnit != "tokens" {
		t.Fatalf("pricing was not applied: %+v", sess)
	}

	// Same price again is fine — opens are idempotent.
	if _, _, err := st.OpenSession(Session{
		WorkID: "w1", PricePerWorkUnitWei: "7", PerUnits: 1000, WorkUnit: "tokens",
	}); err != nil {
		t.Fatalf("re-opening at the same price must be idempotent: %v", err)
	}
	// A different price is refused.
	if _, _, err := st.OpenSession(Session{
		WorkID: "w1", PricePerWorkUnitWei: "70", PerUnits: 1000, WorkUnit: "tokens",
	}); !errors.Is(err, ErrPricingConflict) {
		t.Fatalf("re-pricing = %v; want ErrPricingConflict", err)
	}
}
