package payment

import (
	"context"
	"math/big"
	"path/filepath"
	"testing"
)

// A mock with a state path must behave like the real daemon across a
// process boundary: the session is still open, so OpenSession reports
// AlreadyOpen and debits keep working. Without that, paid-session
// recovery can never take the rebind branch.
func TestMockPersistenceSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	ctx := context.Background()

	first := NewMock()
	if err := first.EnablePersistence(path); err != nil {
		t.Fatal(err)
	}
	if _, err := first.OpenSession(ctx, OpenSessionRequest{
		WorkID: "w1", Capability: "c", Offering: "o",
		PricePerWorkUnitWei: big.NewInt(10), WorkUnit: "u",
	}); err != nil {
		t.Fatal(err)
	}
	res, err := first.ProcessPayment(ctx, ProcessPaymentRequest{WorkID: "w1", PaymentBytes: []byte("pay")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.DebitBalance(ctx, DebitBalanceRequest{
		Sender: res.Sender, WorkID: "w1", WorkUnits: 3, DebitSeq: 1,
	}); err != nil {
		t.Fatal(err)
	}

	// New process, same state file.
	second := NewMock()
	if err := second.EnablePersistence(path); err != nil {
		t.Fatal(err)
	}
	open, err := second.OpenSession(ctx, OpenSessionRequest{
		WorkID: "w1", Capability: "c", Offering: "o",
		PricePerWorkUnitWei: big.NewInt(10), WorkUnit: "u",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !open.AlreadyOpen {
		t.Fatal("session did not survive: AlreadyOpen=false (rebind branch unreachable)")
	}
	if _, err := second.DebitBalance(ctx, DebitBalanceRequest{
		Sender: res.Sender, WorkID: "w1", WorkUnits: 4, DebitSeq: 2,
	}); err != nil {
		t.Fatalf("debit after restart: %v", err)
	}
	// Debit-seq idempotency must survive too, or a resumed broker
	// re-charges work the daemon already recorded.
	if _, err := second.DebitBalance(ctx, DebitBalanceRequest{
		Sender: res.Sender, WorkID: "w1", WorkUnits: 3, DebitSeq: 1,
	}); err != nil {
		t.Fatalf("replayed debit_seq after restart: %v", err)
	}
}

// Without a state path the mock stays amnesiac — the configuration that
// exercises the fail-closed half of recovery.
func TestMockWithoutPersistenceIsAmnesiac(t *testing.T) {
	ctx := context.Background()
	first := NewMock()
	if _, err := first.OpenSession(ctx, OpenSessionRequest{
		WorkID: "w1", Capability: "c", Offering: "o",
		PricePerWorkUnitWei: big.NewInt(10), WorkUnit: "u",
	}); err != nil {
		t.Fatal(err)
	}
	second := NewMock()
	open, err := second.OpenSession(ctx, OpenSessionRequest{
		WorkID: "w1", Capability: "c", Offering: "o",
		PricePerWorkUnitWei: big.NewInt(10), WorkUnit: "u",
	})
	if err != nil {
		t.Fatal(err)
	}
	if open.AlreadyOpen {
		t.Fatal("amnesiac mock reported AlreadyOpen")
	}
}
