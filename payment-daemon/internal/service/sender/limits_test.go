package sender_test

import (
	"math/big"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/sender"
)

func TestLimitsDescribeIsStableAndComplete(t *testing.T) {
	if got := (sender.Limits{}).Describe(); !strings.Contains(got, "max_payment_wei=unlimited") || !strings.Contains(got, "max_ticket_face_value_wei=unlimited") || !strings.Contains(got, "max_authorization_wei=unlimited") || !strings.Contains(got, "max_price_per_unit=(none set") {
		t.Fatalf("empty limits description = %q", got)
	}
	configured := sender.Limits{
		MaxPaymentWei:         big.NewInt(10),
		MaxTicketFaceValueWei: big.NewInt(20),
		MaxAuthorizationWei:   big.NewInt(100),
		MaxPricePerUnit: map[string]*big.Int{
			"z_units": big.NewInt(3),
			"a_units": big.NewInt(2),
		},
	}
	if got := configured.Describe(); got != "max_payment_wei=10 max_ticket_face_value_wei=20 max_authorization_wei=100 max_price_per_unit=a_units=2,z_units=3" {
		t.Fatalf("configured limits description = %q", got)
	}
}

func TestLimitsSignedTicketBatchChecksEVAndWinningExposure(t *testing.T) {
	l := sender.Limits{MaxPaymentWei: big.NewInt(100), MaxTicketFaceValueWei: big.NewInt(1000)}
	if err := l.CheckSignedTicketBatch(big.NewInt(100), big.NewInt(1000)); err != nil {
		t.Fatalf("batch at both limits must pass: %v", err)
	}
	if err := l.CheckSignedTicketBatch(big.NewInt(101), big.NewInt(1000)); err == nil || !strings.Contains(err.Error(), "actual ticket expected value") {
		t.Fatalf("actual EV overage error = %v", err)
	}
	if err := l.CheckSignedTicketBatch(big.NewInt(100), big.NewInt(1001)); err == nil || !strings.Contains(err.Error(), "face_value") {
		t.Fatalf("winning exposure overage error = %v", err)
	}
}

func TestLimitsCircuitBreaker(t *testing.T) {
	l := sender.Limits{MaxPaymentWei: big.NewInt(1000)}
	if err := l.CheckMint("tokens", big.NewInt(1), 1, big.NewInt(1000)); err != nil {
		t.Fatalf("funding at the cap must pass: %v", err)
	}
	if err := l.CheckMint("tokens", big.NewInt(1), 1, big.NewInt(1001)); err == nil {
		t.Fatal("funding above the cap was authorized")
	}
}

func TestLimitsAuthorizationIndependentFromMint(t *testing.T) {
	l := sender.Limits{MaxPaymentWei: big.NewInt(100), MaxAuthorizationWei: big.NewInt(10_000)}
	if err := l.CheckMint("tokens", big.NewInt(1), 1, big.NewInt(100)); err != nil {
		t.Fatal(err)
	}
	if err := l.CheckAuthorization(big.NewInt(10_000)); err != nil {
		t.Fatal(err)
	}
	if err := l.CheckAuthorization(big.NewInt(10_001)); err == nil {
		t.Fatal("authorization above its independent cap accepted")
	}
}

// TestLimitsRateCeilingIsPerWorkUnit is the case a single number cannot
// serve: an expensive unit and a cheap one, orders of magnitude apart,
// neither constraining the other.
func TestLimitsRateCeilingIsPerWorkUnit(t *testing.T) {
	l := sender.Limits{
		MaxPaymentWei: big.NewInt(1_000_000_000_000_000_000),
		MaxPricePerUnit: map[string]*big.Int{
			"tokens":        big.NewInt(10),
			"video_seconds": big.NewInt(2_000_000_000_000_000),
		},
	}
	if err := l.CheckMint("tokens", big.NewInt(10), 1, big.NewInt(100)); err != nil {
		t.Fatalf("tokens at its ceiling must pass: %v", err)
	}
	if err := l.CheckMint("tokens", big.NewInt(11), 1, big.NewInt(100)); err == nil {
		t.Fatal("tokens above its ceiling was authorized")
	}
	// The video price would be absurd for tokens and is fine here.
	if err := l.CheckMint("video_seconds", big.NewInt(2_000_000_000_000_000), 1, big.NewInt(100)); err != nil {
		t.Fatalf("video_seconds at its own ceiling must pass: %v", err)
	}
}

// TestLimitsUnlistedUnitKeepsTheBreaker: a workload with no rate policy
// still cannot breach the circuit breaker, so adding a capability never
// leaves a gateway completely unguarded.
func TestLimitsUnlistedUnitKeepsTheBreaker(t *testing.T) {
	l := sender.Limits{
		MaxPaymentWei:   big.NewInt(1000),
		MaxPricePerUnit: map[string]*big.Int{"tokens": big.NewInt(10)},
	}
	if err := l.CheckMint("brand_new_unit", big.NewInt(999999), 1, big.NewInt(10)); err != nil {
		t.Fatalf("an unlisted unit has no rate policy: %v", err)
	}
	if err := l.CheckMint("brand_new_unit", big.NewInt(999999), 1, big.NewInt(5000)); err == nil {
		t.Fatal("an unlisted unit escaped the circuit breaker")
	}
}

// TestLimitsRateCeilingScalesByDenominator: a price quoted per 1000 units
// must be compared against 1000x the per-unit ceiling, or every
// sub-wei-per-unit offering trips a limit it does not actually breach.
func TestLimitsRateCeilingScalesByDenominator(t *testing.T) {
	l := sender.Limits{MaxPricePerUnit: map[string]*big.Int{"tokens": big.NewInt(10)}}
	// 5000 wei per 1000 tokens = 5 wei/token, under the ceiling of 10.
	if err := l.CheckMint("tokens", big.NewInt(5000), 1000, big.NewInt(1)); err != nil {
		t.Fatalf("5 wei/token must pass a 10 wei/token ceiling: %v", err)
	}
	// 20000 per 1000 = 20 wei/token, over.
	if err := l.CheckMint("tokens", big.NewInt(20000), 1000, big.NewInt(1)); err == nil {
		t.Fatal("20 wei/token passed a 10 wei/token ceiling")
	}
}

func TestParseMaxPricePerUnit(t *testing.T) {
	got, err := sender.ParseMaxPricePerUnit("tokens=10, video_seconds=2000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if got["tokens"].Int64() != 10 || got["video_seconds"].String() != "2000000000000000" {
		t.Fatalf("parsed = %v", got)
	}
	for _, bad := range []string{"tokens", "tokens=", "=10", "tokens=abc", "tokens=-1"} {
		if _, err := sender.ParseMaxPricePerUnit(bad); err == nil {
			t.Fatalf("%q was accepted", bad)
		}
	}
}
