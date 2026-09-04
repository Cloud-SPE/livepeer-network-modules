package settlement_test

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"testing"
	"time"

	cerrors "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/errors"

	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/devclock"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/escrow"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/settlement"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
)

func revertErr() error {
	return fmt.Errorf("%w: tx 0xabc: %w", providers.ErrRedemptionReverted,
		cerrors.New(cerrors.ClassReverted, "tx.reverted", "transaction reverted on-chain"))
}

func TestIsNonRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"ticket used", settlement.ErrTicketUsed, true},
		{"expired", settlement.ErrTicketExpired, true},
		{"face value too low", settlement.ErrFaceValueTooLow, true},
		{"sender funds", settlement.ErrInsufficientFunds, false},
		{"broker already used", fmt.Errorf("redeem: %w", providers.ErrTicketAlreadyUsed), true},
		{"broker reverted", fmt.Errorf("redeem: %w", revertErr()), true},
		{"classified revert alone", cerrors.New(cerrors.ClassReverted, "tx.reverted", "x"), true},
		{"creationRound revert string", errors.New("execution reverted: creationRound does not have a block hash"), true},
		{"transient", cerrors.New(cerrors.ClassTransient, "rpc.timeout", "x"), false},
		{"not found", cerrors.New(cerrors.ClassNotFound, "rpc.not_found", "x"), false},
		{"circuit open", cerrors.New(cerrors.ClassCircuitOpen, "rpc.circuit_open", "x"), false},
		{"permanent (operator-fixable)", cerrors.New(cerrors.ClassPermanent, "tx.sign_failed", "x"), false},
		{"insufficient wallet funds", cerrors.New(cerrors.ClassInsufficientFunds, "tx.insufficient_funds", "x"), false},
		{"nonce past", cerrors.New(cerrors.ClassNoncePast, "tx.nonce_past", "x"), false},
		{"cancelled tick", fmt.Errorf("wait: %w", context.Canceled), false},
		{"plain error", errors.New("transaction failed"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := settlement.IsNonRetryable(tc.err); got != tc.want {
				t.Fatalf("IsNonRetryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// newSettlement wires a store with one pending ticket and a broker that
// answers RedeemWinningTicket with redeemErr.
func newSettlement(t *testing.T, redeemErr error) (*settlement.Settlement, *store.Store, *fakeBroker, []byte) {
	t.Helper()
	st := setupStore(t)
	hash := newPending(t, st, 1_000_000_000, 100)
	broker := &fakeBroker{redeemError: redeemErr}
	clock := devclock.New()
	esc := escrow.New(broker, clock, escrow.Config{})
	s := settlement.New(st, broker, fakeGas{wei: big.NewInt(100)}, clock, esc, settlement.Config{
		RedeemGas:      500_000,
		ValidityWindow: 100,
	})
	return s, st, broker, hash
}

func pendingCount(t *testing.T, st *store.Store) int {
	t.Helper()
	pend, err := st.PendingRedemptions()
	if err != nil {
		t.Fatal(err)
	}
	return len(pend)
}

func TestRedeemNext_BrokerAlreadyUsedDrains(t *testing.T) {
	s, st, _, hash := newSettlement(t, providers.ErrTicketAlreadyUsed)
	got, err := s.RedeemNext(context.Background())
	if !errors.Is(err, settlement.ErrTicketUsed) {
		t.Fatalf("err = %v, want ErrTicketUsed", err)
	}
	if string(got) != string(hash) {
		t.Fatal("hash mismatch")
	}
	if n := pendingCount(t, st); n != 0 {
		t.Fatalf("pending after already-used = %d, want 0", n)
	}
	if tx, err := st.RedeemedTxHash(hash); err != nil || len(tx) != 32 {
		t.Fatalf("drained ticket should be marked redeemed with a zero hash: %x %v", tx, err)
	}
}

func TestRedeemNext_RevertDrainsAndIsTerminal(t *testing.T) {
	s, st, _, _ := newSettlement(t, revertErr())
	_, err := s.RedeemNext(context.Background())
	if err == nil || !settlement.IsNonRetryable(err) {
		t.Fatalf("revert must be terminal: %v", err)
	}
	if !errors.Is(err, providers.ErrRedemptionReverted) {
		t.Fatalf("sentinel lost through the wrap: %v", err)
	}
	if cerrors.Classify(err).Class != cerrors.ClassReverted {
		t.Fatalf("class = %s", cerrors.Classify(err).Class)
	}
	if n := pendingCount(t, st); n != 0 {
		t.Fatalf("reverted ticket still queued (%d)", n)
	}
}

func TestRedeemNext_TransientStaysQueued(t *testing.T) {
	transient := cerrors.New(cerrors.ClassTransient, "rpc.timeout", "endpoint timed out")
	s, st, _, _ := newSettlement(t, transient)
	_, err := s.RedeemNext(context.Background())
	if err == nil || settlement.IsNonRetryable(err) {
		t.Fatalf("transient must be retryable: %v", err)
	}
	if !errors.Is(err, transient) {
		t.Fatalf("classification lost: %v", err)
	}
	if n := pendingCount(t, st); n != 1 {
		t.Fatalf("pending after transient = %d, want 1", n)
	}
	// A cancelled tick is the same story.
	s2, st2, _, _ := newSettlement(t, fmt.Errorf("wait: %w", context.DeadlineExceeded))
	if _, err := s2.RedeemNext(context.Background()); err == nil || settlement.IsNonRetryable(err) {
		t.Fatalf("cancelled tick must be retryable: %v", err)
	}
	if n := pendingCount(t, st2); n != 1 {
		t.Fatalf("pending after cancelled tick = %d, want 1", n)
	}
}

func TestRedeemNext_ExpiredRevertStringDrains(t *testing.T) {
	s, st, _, _ := newSettlement(t, errors.New("execution reverted: creationRound does not have a block hash"))
	_, err := s.RedeemNext(context.Background())
	if !errors.Is(err, settlement.ErrTicketExpired) {
		t.Fatalf("err = %v, want ErrTicketExpired", err)
	}
	if n := pendingCount(t, st); n != 0 {
		t.Fatalf("pending = %d", n)
	}
}

func TestRedeemNext_SenderFundsTooLowStaysQueued(t *testing.T) {
	st := setupStore(t)
	newPending(t, st, 1_000_000_000, 100)
	broker := &fakeBroker{senderInfo: &providers.SenderInfo{
		Deposit: big.NewInt(1),
		Reserve: &providers.Reserve{FundsRemaining: big.NewInt(1), Claimed: map[string]*big.Int{}},
	}}
	clock := devclock.New()
	esc := escrow.New(broker, clock, escrow.Config{})
	s := settlement.New(st, broker, fakeGas{wei: big.NewInt(100)}, clock, esc, settlement.Config{RedeemGas: 500_000, ValidityWindow: 100})
	_, err := s.RedeemNext(context.Background())
	if !errors.Is(err, settlement.ErrInsufficientFunds) {
		t.Fatalf("err = %v", err)
	}
	if n := pendingCount(t, st); n != 1 {
		t.Fatalf("pending = %d, want 1 (sender may top up)", n)
	}
	if len(broker.redeemed) != 0 {
		t.Fatal("redeem attempted despite insufficient sender funds")
	}
}

func TestRunAndStop(t *testing.T) {
	s, st, broker, _ := newSettlement(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx, 2*time.Millisecond); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for pendingCount(t, st) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("loop never redeemed the pending ticket")
		}
		time.Sleep(2 * time.Millisecond)
	}
	s.Stop()
	s.Stop() // idempotent
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Stop")
	}
	if len(broker.redeemed) != 1 {
		t.Fatalf("redeemed = %d", len(broker.redeemed))
	}

	// ctx cancellation also ends the loop; a non-positive interval is
	// replaced by the default rather than spinning.
	s2, _, _, _ := newSettlement(t, nil)
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan struct{})
	go func() { s2.Run(ctx2, 0); close(done2) }()
	cancel2()
	select {
	case <-done2:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return on ctx cancel")
	}
}
