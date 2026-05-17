package store

import (
	"math/big"
	"path/filepath"
	"testing"
)

func TestRoundRevenue(t *testing.T) {
	st := openTestStore(t)

	ticketA := &SignedTicket{
		Sender:        bytes20(0x01),
		FaceValue:     big.NewInt(1000),
		CreationRound: 120,
	}
	ticketB := &SignedTicket{
		Sender:        bytes20(0x02),
		FaceValue:     big.NewInt(2000),
		CreationRound: 120,
	}
	hashA := hash32(0xaa)
	hashB := hash32(0xbb)

	if _, err := st.EnqueueRedemption(hashA, ticketA); err != nil {
		t.Fatalf("EnqueueRedemption(ticketA) error = %v", err)
	}
	if _, err := st.EnqueueRedemption(hashB, ticketB); err != nil {
		t.Fatalf("EnqueueRedemption(ticketB) error = %v", err)
	}
	if err := st.MarkRedeemed(hashA, hash32(0x11), ticketA, 124); err != nil {
		t.Fatalf("MarkRedeemed(ticketA) error = %v", err)
	}
	if err := st.MarkRedeemed(hashB, make([]byte, 32), ticketB, 124); err != nil {
		t.Fatalf("MarkRedeemed(ticketB) error = %v", err)
	}

	revenue, count, err := st.RoundRevenue(124)
	if err != nil {
		t.Fatalf("RoundRevenue() error = %v", err)
	}
	if revenue.Cmp(big.NewInt(1000)) != 0 {
		t.Fatalf("revenue = %s; want 1000", revenue.String())
	}
	if count != 1 {
		t.Fatalf("count = %d; want 1", count)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func bytes20(b byte) []byte {
	out := make([]byte, 20)
	for i := range out {
		out[i] = b
	}
	return out
}

func hash32(b byte) []byte {
	out := make([]byte, 32)
	for i := range out {
		out[i] = b
	}
	return out
}
