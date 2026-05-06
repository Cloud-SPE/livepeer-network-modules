package settlement_test

import (
	"context"
	"errors"
	"math/big"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/devclock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service/escrow"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service/settlement"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/store"
)

// fakeBroker lets tests pin IsUsedTicket / Redeem behavior + observe
// invocations.
type fakeBroker struct {
	mu sync.Mutex

	used         map[string]bool
	redeemError  error
	redeemed     [][]byte
	senderInfo   *providers.SenderInfo
}

func (f *fakeBroker) GetSenderInfo(_ context.Context, _ []byte) (*providers.SenderInfo, error) {
	if f.senderInfo == nil {
		return &providers.SenderInfo{
			Deposit: big.NewInt(1_000_000_000),
			Reserve: &providers.Reserve{FundsRemaining: big.NewInt(1_000_000_000), Claimed: map[string]*big.Int{}},
		}, nil
	}
	return f.senderInfo, nil
}
func (f *fakeBroker) IsUsedTicket(_ context.Context, ticketHash []byte) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.used[string(ticketHash)], nil
}
func (f *fakeBroker) RedeemWinningTicket(_ context.Context, _ *providers.Ticket, _ []byte, _ *big.Int) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.redeemError != nil {
		return nil, f.redeemError
	}
	stamp := make([]byte, 32)
	stamp[31] = byte(len(f.redeemed) + 1)
	f.redeemed = append(f.redeemed, stamp)
	return stamp, nil
}

type fakeGas struct{ wei *big.Int }

func (g fakeGas) Current() *big.Int { return g.wei }

func setupStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newPending(t *testing.T, st *store.Store, faceValue int64, round int64) []byte {
	t.Helper()
	hash := make([]byte, 32)
	hash[0] = byte(faceValue)
	hash[1] = byte(round)
	tk := &store.SignedTicket{
		Recipient:         make([]byte, 20),
		Sender:            make([]byte, 20),
		FaceValue:         big.NewInt(faceValue),
		WinProb:           big.NewInt(1),
		SenderNonce:       1,
		RecipientRandHash: make([]byte, 32),
		CreationRound:     round,
		CreationRoundHash: make([]byte, 32),
		Sig:               make([]byte, 65),
		RecipientRand:     big.NewInt(99),
	}
	if _, err := st.EnqueueRedemption(hash, tk); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return hash
}

func TestRedeemNext_HappyPath(t *testing.T) {
	st := setupStore(t)
	hash := newPending(t, st, 1_000_000_000, 100)

	broker := &fakeBroker{}
	gas := fakeGas{wei: big.NewInt(100)} // 500k * 100 = 5e7 < faceValue
	clock := devclock.New()
	esc := escrow.New(broker, clock, escrow.Config{})
	s := settlement.New(st, broker, gas, clock, esc, settlement.Config{
		RedeemGas:      500_000,
		ValidityWindow: 100,
	})

	got, err := s.RedeemNext(context.Background())
	if err != nil {
		t.Fatalf("RedeemNext: %v", err)
	}
	if string(got) != string(hash) {
		t.Errorf("returned hash mismatch")
	}

	// Pending must now be empty.
	pend, _ := st.PendingRedemptions()
	if len(pend) != 0 {
		t.Errorf("pending after redeem = %d; want 0", len(pend))
	}
	if len(broker.redeemed) != 1 {
		t.Errorf("broker redeemed calls = %d; want 1", len(broker.redeemed))
	}
}

func TestRedeemNext_TicketUsedDrains(t *testing.T) {
	st := setupStore(t)
	hash := newPending(t, st, 1_000_000_000, 100)

	broker := &fakeBroker{used: map[string]bool{string(hash): true}}
	gas := fakeGas{wei: big.NewInt(100)}
	clock := devclock.New()
	esc := escrow.New(broker, clock, escrow.Config{})
	s := settlement.New(st, broker, gas, clock, esc, settlement.Config{
		RedeemGas:      500_000,
		ValidityWindow: 100,
	})

	_, err := s.RedeemNext(context.Background())
	if !errors.Is(err, settlement.ErrTicketUsed) {
		t.Errorf("err = %v; want ErrTicketUsed", err)
	}
	pend, _ := st.PendingRedemptions()
	if len(pend) != 0 {
		t.Errorf("pending after used drain = %d; want 0", len(pend))
	}
	if len(broker.redeemed) != 0 {
		t.Errorf("broker should not have submitted; got %d", len(broker.redeemed))
	}
}

func TestRedeemNext_FaceValueTooLowDrains(t *testing.T) {
	st := setupStore(t)
	newPending(t, st, 100, 100) // tiny face value

	broker := &fakeBroker{}
	gas := fakeGas{wei: big.NewInt(1_000_000)} // gas cost 5e11 ≫ 100 face
	clock := devclock.New()
	esc := escrow.New(broker, clock, escrow.Config{})
	s := settlement.New(st, broker, gas, clock, esc, settlement.Config{
		RedeemGas:      500_000,
		ValidityWindow: 100,
	})

	_, err := s.RedeemNext(context.Background())
	if !errors.Is(err, settlement.ErrFaceValueTooLow) {
		t.Errorf("err = %v; want ErrFaceValueTooLow", err)
	}
	pend, _ := st.PendingRedemptions()
	if len(pend) != 0 {
		t.Errorf("pending after low-fv drain = %d; want 0", len(pend))
	}
}

func TestRedeemNext_ExpiredDrains(t *testing.T) {
	st := setupStore(t)
	newPending(t, st, 1_000_000_000, 1) // CreationRound=1; clock=10 default

	broker := &fakeBroker{}
	gas := fakeGas{wei: big.NewInt(100)}
	clock := devclock.New()
	for i := 0; i < 12; i++ {
		clock.Tick(100) // each Tick crossing a 100-block boundary adds 1 round
	}
	esc := escrow.New(broker, clock, escrow.Config{})
	s := settlement.New(st, broker, gas, clock, esc, settlement.Config{
		RedeemGas:      500_000,
		ValidityWindow: 2,
	})

	_, err := s.RedeemNext(context.Background())
	if !errors.Is(err, settlement.ErrTicketExpired) {
		t.Errorf("err = %v; want ErrTicketExpired", err)
	}
	pend, _ := st.PendingRedemptions()
	if len(pend) != 0 {
		t.Errorf("pending after expired drain = %d; want 0", len(pend))
	}
}

func TestRedeemNext_EmptyQueue(t *testing.T) {
	st := setupStore(t)
	broker := &fakeBroker{}
	gas := fakeGas{wei: big.NewInt(100)}
	clock := devclock.New()
	esc := escrow.New(broker, clock, escrow.Config{})
	s := settlement.New(st, broker, gas, clock, esc, settlement.Config{})

	hash, err := s.RedeemNext(context.Background())
	if err != nil {
		t.Fatalf("RedeemNext on empty: %v", err)
	}
	if hash != nil {
		t.Errorf("hash = %x; want nil", hash)
	}
}

func TestEscrowRebuildAfterRestart(t *testing.T) {
	st := setupStore(t)
	newPending(t, st, 1_000_000, 100)
	newPending(t, st, 2_000_000, 100)

	broker := &fakeBroker{}
	clock := devclock.New()
	esc := escrow.New(broker, clock, escrow.Config{})
	if err := esc.Rebuild(st); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	// Both tickets share sender = make([]byte,20). Pending = 3_000_000.
	got := esc.Pending(make([]byte, 20))
	if want := big.NewInt(3_000_000); got.Cmp(want) != 0 {
		t.Errorf("pending after rebuild = %s; want %s", got, want)
	}
}
