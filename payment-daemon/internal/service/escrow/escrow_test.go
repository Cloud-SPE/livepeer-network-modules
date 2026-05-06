package escrow_test

import (
	"context"
	"math/big"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/devbroker"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/devclock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service/escrow"
)

// fakeBroker lets tests pin SenderInfo / claimed values.
type fakeBroker struct {
	*devbroker.DevBroker
	deposit        *big.Int
	fundsRemaining *big.Int
	claimedByMe    *big.Int
	claimantKey    string
}

func (f *fakeBroker) GetSenderInfo(_ context.Context, _ []byte) (*providers.SenderInfo, error) {
	return &providers.SenderInfo{
		Deposit: new(big.Int).Set(f.deposit),
		Reserve: &providers.Reserve{
			FundsRemaining: new(big.Int).Set(f.fundsRemaining),
			Claimed:        map[string]*big.Int{f.claimantKey: new(big.Int).Set(f.claimedByMe)},
		},
	}, nil
}

func TestMaxFloat_NoPending(t *testing.T) {
	claimant := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa,
		0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11, 0x22, 0x33, 0x44}
	claimantKey := "0x" + strings.ToLower("11223344556677889900000000000000000000000000000000")[:0] + // build the canonical hex
		strings.ToLower("11223344556677889900112233445566778899AA")[:0]
	_ = claimantKey
	canonKey := "0x" + strings.ToLower(toHex(claimant))
	b := &fakeBroker{
		DevBroker:      devbroker.New(),
		deposit:        big.NewInt(100),
		fundsRemaining: big.NewInt(1000),
		claimedByMe:    big.NewInt(0),
		claimantKey:    canonKey,
	}
	clock := devclock.New() // poolSize = 100

	e := escrow.New(b, clock, escrow.Config{Claimant: claimant})
	got, err := e.MaxFloat(context.Background(), []byte("sender"))
	if err != nil {
		t.Fatalf("MaxFloat: %v", err)
	}
	// reserveAlloc = (1000+0)/100 - 0 = 10. deposit = 100. pending = 0.
	want := big.NewInt(110)
	if got.Cmp(want) != 0 {
		t.Errorf("MaxFloat = %s; want %s", got, want)
	}
}

func TestMaxFloat_3to1IgnoresPending(t *testing.T) {
	claimant := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa,
		0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11, 0x22, 0x33, 0x44}
	canonKey := "0x" + strings.ToLower(toHex(claimant))
	b := &fakeBroker{
		DevBroker:      devbroker.New(),
		deposit:        big.NewInt(300),
		fundsRemaining: big.NewInt(1000),
		claimedByMe:    big.NewInt(0),
		claimantKey:    canonKey,
	}
	clock := devclock.New()

	e := escrow.New(b, clock, escrow.Config{Claimant: claimant})
	sender := []byte("snd-A20bytes!!!!!!!!")
	e.SubFloat(sender, big.NewInt(50)) // deposit/pending = 6 ≥ 3 → ignore pending

	got, _ := e.MaxFloat(context.Background(), sender)
	// reserveAlloc = 10, deposit = 300. With pending ignored: 310.
	if want := big.NewInt(310); got.Cmp(want) != 0 {
		t.Errorf("MaxFloat = %s; want %s (3:1 ignores pending)", got, want)
	}
}

func TestMaxFloat_PendingSubtractsBelowRatio(t *testing.T) {
	claimant := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa,
		0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11, 0x22, 0x33, 0x44}
	canonKey := "0x" + strings.ToLower(toHex(claimant))
	b := &fakeBroker{
		DevBroker:      devbroker.New(),
		deposit:        big.NewInt(100),
		fundsRemaining: big.NewInt(1000),
		claimedByMe:    big.NewInt(0),
		claimantKey:    canonKey,
	}
	clock := devclock.New()
	e := escrow.New(b, clock, escrow.Config{Claimant: claimant})
	sender := []byte("snd-A20bytes!!!!!!!!")
	e.SubFloat(sender, big.NewInt(50)) // deposit/pending = 2 < 3 → subtract

	got, _ := e.MaxFloat(context.Background(), sender)
	// reserveAlloc + deposit - pending = 10 + 100 - 50 = 60.
	if want := big.NewInt(60); got.Cmp(want) != 0 {
		t.Errorf("MaxFloat = %s; want %s (pending subtracted)", got, want)
	}
}

func TestAddFloat_Underflow(t *testing.T) {
	claimant := []byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa,
		0xbb, 0xcc, 0xdd, 0xee, 0xff, 0x00, 0x11, 0x22, 0x33, 0x44}
	canonKey := "0x" + strings.ToLower(toHex(claimant))
	b := &fakeBroker{
		DevBroker:      devbroker.New(),
		deposit:        big.NewInt(100),
		fundsRemaining: big.NewInt(0),
		claimedByMe:    big.NewInt(0),
		claimantKey:    canonKey,
	}
	clock := devclock.New()
	e := escrow.New(b, clock, escrow.Config{Claimant: claimant})

	if err := e.AddFloat([]byte("sender-1"), big.NewInt(10)); err == nil {
		t.Error("AddFloat(10) on zero pending: expected ErrPendingUnderflow")
	}

	e.SubFloat([]byte("sender-1"), big.NewInt(20))
	if err := e.AddFloat([]byte("sender-1"), big.NewInt(15)); err != nil {
		t.Errorf("AddFloat(15) on pending=20: %v", err)
	}
	if got := e.Pending([]byte("sender-1")); got.Cmp(big.NewInt(5)) != 0 {
		t.Errorf("pending = %s; want 5", got)
	}
}

func toHex(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[2*i] = digits[v>>4]
		out[2*i+1] = digits[v&0x0f]
	}
	return string(out)
}
