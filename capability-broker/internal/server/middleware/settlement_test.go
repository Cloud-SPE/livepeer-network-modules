package middleware

import (
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

// makePaymentBytes builds a proto-marshaled Payment with an ExpectedPrice
// whose Constraint matches the format the sender daemon writes (see
// payment-daemon/internal/service/sender/sender.go:expectedPriceConstraint).
func makePaymentBytes(t *testing.T, pricePerUnit int64, estimatedUnits uint64) []byte {
	t.Helper()
	return makePaymentBytesPerUnits(t, pricePerUnit, 1, estimatedUnits)
}

// makePaymentBytesPerUnits builds a payment whose price is quoted per
// many units — the case where flooring and ceiling diverge.
func makePaymentBytesPerUnits(t *testing.T, pricePerUnit int64, perUnits int64, estimatedUnits uint64) []byte {
	t.Helper()
	constraint := fmt.Sprintf(
		"cap=cap;off=off;wu=tokens;est=%d;qid=quote-1;qv=1;cfp=%x;rfp=%x",
		estimatedUnits,
		[]byte{0xaa, 0xbb},
		[]byte{0xcc, 0xdd},
	)
	pay := &pb.Payment{
		ExpectedPrice: &pb.PriceInfo{
			PricePerUnit:  pricePerUnit,
			PixelsPerUnit: perUnits,
			Constraint:    constraint,
		},
	}
	raw, err := proto.Marshal(pay)
	if err != nil {
		t.Fatalf("marshal Payment: %v", err)
	}
	return raw
}

func TestBuildSettlementRecord_Outcomes(t *testing.T) {
	const pricePerUnit int64 = 10 // wei per unit
	cases := []struct {
		name              string
		actualUnits       uint64
		fundedValueWei    int64
		terminationReason string
		wantOutcome       pb.SettlementRecord_SettlementOutcome
	}{
		{
			name:           "exact",
			actualUnits:    100,
			fundedValueWei: 1000,
			wantOutcome:    pb.SettlementRecord_EXACT,
		},
		{
			name:           "underfunded",
			actualUnits:    100,
			fundedValueWei: 500,
			wantOutcome:    pb.SettlementRecord_UNDERFUNDED,
		},
		{
			name:           "overfunded",
			actualUnits:    100,
			fundedValueWei: 2000,
			wantOutcome:    pb.SettlementRecord_OVERFUNDED,
		},
		{
			name:              "stopped_at_budget_overrides_underfunded",
			actualUnits:       100,
			fundedValueWei:    500,
			terminationReason: livepeerheader.ErrInsufficientBalance,
			wantOutcome:       pb.SettlementRecord_STOPPED_AT_BUDGET,
		},
		{
			name:              "stopped_at_budget_overrides_exact",
			actualUnits:       100,
			fundedValueWei:    1000,
			terminationReason: livepeerheader.ErrInsufficientBalance,
			wantOutcome:       pb.SettlementRecord_STOPPED_AT_BUDGET,
		},
		{
			name:              "unrelated_termination_does_not_alter_outcome",
			actualUnits:       100,
			fundedValueWei:    500,
			terminationReason: "some_other_reason",
			wantOutcome:       pb.SettlementRecord_UNDERFUNDED,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			paymentBytes := makePaymentBytes(t, pricePerUnit, 100)
			got := buildSettlementRecord(
				paymentBytes,
				big.NewInt(tc.fundedValueWei),
				tc.actualUnits,
				"tokens",
				tc.terminationReason,
				testIdentity(),
			)
			if got == nil {
				t.Fatalf("buildSettlementRecord returned nil")
			}
			if got.GetOutcome() != tc.wantOutcome {
				t.Fatalf("outcome = %v, want %v", got.GetOutcome(), tc.wantOutcome)
			}
			if got.GetActualUnits() != tc.actualUnits {
				t.Fatalf("actual_units = %d, want %d", got.GetActualUnits(), tc.actualUnits)
			}
			if got.GetBilledUnits() != tc.actualUnits {
				t.Fatalf("billed_units = %d, want %d (placeholder: billed==actual)", got.GetBilledUnits(), tc.actualUnits)
			}
			if got.GetAcceptedQuoteRef().GetQuoteId() != "quote-1" {
				t.Fatalf("quote_id = %q, want %q", got.GetAcceptedQuoteRef().GetQuoteId(), "quote-1")
			}
			if got.GetWorkUnitName() != "tokens" {
				t.Fatalf("work_unit_name = %q, want %q", got.GetWorkUnitName(), "tokens")
			}
		})
	}
}

func TestBuildSettlementRecord_NilForUnparseablePayment(t *testing.T) {
	if got := buildSettlementRecord([]byte("not-a-proto"), big.NewInt(0), 0, "tokens", "", testIdentity()); got != nil {
		t.Fatalf("expected nil for unparseable payment, got %+v", got)
	}
}

func TestBuildSettlementRecord_NilWhenExpectedPriceMissing(t *testing.T) {
	raw, err := proto.Marshal(&pb.Payment{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := buildSettlementRecord(raw, big.NewInt(0), 0, "tokens", "", testIdentity()); got != nil {
		t.Fatalf("expected nil when expected_price missing, got %+v", got)
	}
}

func TestEncodeSettlementRecord_RoundTrip(t *testing.T) {
	paymentBytes := makePaymentBytes(t, 10, 100)
	rec := buildSettlementRecord(paymentBytes, big.NewInt(1000), 100, "tokens", "", testIdentity())
	if rec == nil {
		t.Fatalf("buildSettlementRecord returned nil")
	}
	encoded, err := encodeSettlementRecord(rec)
	if err != nil {
		t.Fatalf("encodeSettlementRecord: %v", err)
	}
	if encoded == "" {
		t.Fatalf("encoded settlement record is empty")
	}
}

// testIdentity is the signed identity every settlement carries: which
// exchange it describes and when it was made.
func testIdentity() SettlementIdentity {
	return SettlementIdentity{
		JobID:    "job_test",
		WorkID:   "work_test",
		IssuedAt: "2026-08-20T12:00:00Z",
	}
}

// TestBilledValueCeilingsWithRemainder is LOC's finding: with
// per_units > 1 the record used integer division, which FLOORS. A
// clearinghouse recomputing the normative rule then disagreed with the
// signed record it was verifying.
func TestBilledValueCeilingsWithRemainder(t *testing.T) {
	// 31 units at 100 wei per 1000 units = 3.1 wei. Ceiling is 4;
	// flooring gives 3, and the remainder is what exposes the bug.
	paymentBytes := makePaymentBytesPerUnits(t, 100, 1000, 31)
	rec := buildSettlementRecord(paymentBytes, big.NewInt(1000), 31, "tokens", "", testIdentity())
	if rec == nil {
		t.Fatal("nil record")
	}
	got := new(big.Int).SetBytes(rec.GetBilledValueWei().GetValue())
	if got.Int64() != 4 {
		t.Fatalf("billed = %s wei; want 4 (ceil), not 3 (floor)", got)
	}
}

// TestSettlementCarriesSignedIdentity: a record that cannot say which
// exchange it describes can be replayed as evidence for another. work_id
// alone is not enough on paid-job — it is the ticket session's rand
// hash, shared by every job minted against that session.
func TestSettlementCarriesSignedIdentity(t *testing.T) {
	paymentBytes := makePaymentBytes(t, 1, 100)
	rec := buildSettlementRecord(paymentBytes, big.NewInt(1000), 100, "tokens", "", SettlementIdentity{
		JobID:    "job_abc",
		WorkID:   "work_shared",
		IssuedAt: "2026-08-20T12:00:00Z",
	})
	if rec.GetJobId() != "job_abc" {
		t.Fatalf("job_id = %q; a settlement must name its exchange", rec.GetJobId())
	}
	if rec.GetWorkId() != "work_shared" {
		t.Fatalf("work_id = %q", rec.GetWorkId())
	}
	if rec.GetIssuedAt() == "" {
		t.Fatal("issued_at is empty; a record with no time cannot be aged out or ordered")
	}
	if _, err := time.Parse(time.RFC3339, rec.GetIssuedAt()); err != nil {
		t.Fatalf("issued_at %q is not RFC3339: %v", rec.GetIssuedAt(), err)
	}
}

// TestCrossJobSettlementReplayIsDetectable: two exchanges on the SAME
// ticket session share a work_id, so job_id is the only thing that
// distinguishes their settlements. Without it the two records would be
// interchangeable.
func TestCrossJobSettlementReplayIsDetectable(t *testing.T) {
	paymentBytes := makePaymentBytes(t, 1, 100)
	shared := "work_shared_by_both_jobs"
	a := buildSettlementRecord(paymentBytes, big.NewInt(1000), 100, "tokens", "", SettlementIdentity{
		JobID: "job_a", WorkID: shared, IssuedAt: "2026-08-20T12:00:00Z",
	})
	b := buildSettlementRecord(paymentBytes, big.NewInt(1000), 100, "tokens", "", SettlementIdentity{
		JobID: "job_b", WorkID: shared, IssuedAt: "2026-08-20T12:00:01Z",
	})
	if a.GetWorkId() != b.GetWorkId() {
		t.Fatal("test premise wrong: these should share a work_id")
	}
	if a.GetJobId() == b.GetJobId() {
		t.Fatal("settlements for different jobs are indistinguishable inside the signature")
	}
}

// A failed debit must be visible in the record, not smoothed over.
//
// The broker delivers work and debits afterwards, so a ledger call can
// fail with the response already gone. The record used to attest the
// MEASURED units regardless, which made a broker whose debit failed
// indistinguishable from one that was paid — a clearinghouse would book
// revenue that never moved, and the failure was invisible exactly when
// it mattered.
func TestBuildSettlementRecord_DebitFailureIsAttested(t *testing.T) {
	paymentBytes := makePaymentBytes(t, 10, 100)

	ident := testIdentity()
	ident.DebitFailed = true
	ident.DebitedUnits = 0 // single-debit path: nothing was taken

	got := buildSettlementRecord(paymentBytes, big.NewInt(1000), 100, "tokens", "", ident)
	if got == nil {
		t.Fatal("buildSettlementRecord returned nil")
	}
	if got.GetOutcome() != pb.SettlementRecord_DEBIT_FAILED {
		t.Fatalf("outcome = %v; want DEBIT_FAILED so a consumer refuses the record",
			got.GetOutcome())
	}
	if got.GetDebitedUnits() != 0 {
		t.Fatalf("debited_units = %d; want 0 — the ledger took nothing",
			got.GetDebitedUnits())
	}
	if got.GetActualUnits() != 100 {
		t.Fatalf("actual_units = %d; want the measurement preserved at 100",
			got.GetActualUnits())
	}
	if v := new(big.Int).SetBytes(got.GetBilledValueWei().GetValue()); v.Sign() != 0 {
		t.Fatalf("billed_value_wei = %s; want 0 — attesting a charge that never moved is "+
			"the whole defect", v)
	}
}

// Interim ticks that DID succeed took real value; a failed FINAL debit
// must not disown them.
func TestBuildSettlementRecord_DebitFailureKeepsInterimDebits(t *testing.T) {
	paymentBytes := makePaymentBytes(t, 10, 100)

	ident := testIdentity()
	ident.DebitFailed = true
	ident.DebitedUnits = 70 // interim ticks covered 70 of 100
	ident.ChargedWei = big.NewInt(700)

	got := buildSettlementRecord(paymentBytes, big.NewInt(1000), 100, "tokens", "", ident)
	if got == nil {
		t.Fatal("buildSettlementRecord returned nil")
	}
	if got.GetOutcome() != pb.SettlementRecord_DEBIT_FAILED {
		t.Fatalf("outcome = %v; want DEBIT_FAILED", got.GetOutcome())
	}
	if got.GetDebitedUnits() != 70 {
		t.Fatalf("debited_units = %d; want 70 — the interim ticks were real debits",
			got.GetDebitedUnits())
	}
	if v := new(big.Int).SetBytes(got.GetBilledValueWei().GetValue()); v.Cmp(big.NewInt(700)) != 0 {
		t.Fatalf("billed_value_wei = %s; want 700, what the ledger actually took", v)
	}
}

// The normal path is unchanged: measured units are debited units.
func TestBuildSettlementRecord_NormalPathDebitsWhatItMeasures(t *testing.T) {
	paymentBytes := makePaymentBytes(t, 10, 100)
	ident := testIdentity()
	ident.DebitedUnits = 100

	got := buildSettlementRecord(paymentBytes, big.NewInt(1000), 100, "tokens", "", ident)
	if got == nil {
		t.Fatal("buildSettlementRecord returned nil")
	}
	if got.GetOutcome() == pb.SettlementRecord_DEBIT_FAILED {
		t.Fatal("a successful exchange must not be marked DEBIT_FAILED")
	}
	if got.GetDebitedUnits() != got.GetActualUnits() {
		t.Fatalf("debited %d != actual %d on the normal path",
			got.GetDebitedUnits(), got.GetActualUnits())
	}
}
