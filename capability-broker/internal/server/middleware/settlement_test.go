package middleware

import (
	"fmt"
	"math/big"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

// makePaymentBytes builds a proto-marshaled Payment with an ExpectedPrice
// whose Constraint matches the format the sender daemon writes (see
// payment-daemon/internal/service/sender/sender.go:expectedPriceConstraint).
func makePaymentBytes(t *testing.T, pricePerUnit int64, estimatedUnits uint64) []byte {
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
			PixelsPerUnit: 1,
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
	if got := buildSettlementRecord([]byte("not-a-proto"), big.NewInt(0), 0, "tokens", ""); got != nil {
		t.Fatalf("expected nil for unparseable payment, got %+v", got)
	}
}

func TestBuildSettlementRecord_NilWhenExpectedPriceMissing(t *testing.T) {
	raw, err := proto.Marshal(&pb.Payment{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := buildSettlementRecord(raw, big.NewInt(0), 0, "tokens", ""); got != nil {
		t.Fatalf("expected nil when expected_price missing, got %+v", got)
	}
}

func TestEncodeSettlementRecord_RoundTrip(t *testing.T) {
	paymentBytes := makePaymentBytes(t, 10, 100)
	rec := buildSettlementRecord(paymentBytes, big.NewInt(1000), 100, "tokens", "")
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
