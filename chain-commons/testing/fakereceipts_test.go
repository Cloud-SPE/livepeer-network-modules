package chaintesting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/receipts"
)

func TestFakeReceipts_InstantSuccess(t *testing.T) {
	r := NewFakeReceipts()
	hash := chain.TxHash{0x01}
	want := &receipts.Receipt{TxHash: hash, Status: 1, Confirmed: true}
	r.Set(hash, want)

	got, err := r.WaitConfirmed(context.Background(), hash, 4)
	if err != nil {
		t.Fatalf("WaitConfirmed: %v", err)
	}
	if got != want {
		t.Errorf("WaitConfirmed returned different receipt")
	}
}

func TestFakeReceipts_InstantError(t *testing.T) {
	r := NewFakeReceipts()
	hash := chain.TxHash{0x02}
	want := errors.New("rpc gone")
	r.SetError(hash, want)

	_, err := r.WaitConfirmed(context.Background(), hash, 4)
	if err != want {
		t.Errorf("WaitConfirmed err = %v, want %v", err, want)
	}
}

func TestFakeReceipts_BlocksUntilSet(t *testing.T) {
	r := NewFakeReceipts()
	hash := chain.TxHash{0x03}

	done := make(chan *receipts.Receipt, 1)
	go func() {
		got, _ := r.WaitConfirmed(context.Background(), hash, 4)
		done <- got
	}()

	// Briefly let WaitConfirmed register itself.
	time.Sleep(10 * time.Millisecond)

	want := &receipts.Receipt{TxHash: hash, Confirmed: true}
	r.Set(hash, want)

	select {
	case got := <-done:
		if got != want {
			t.Errorf("WaitConfirmed returned different receipt")
		}
	case <-time.After(time.Second):
		t.Fatal("WaitConfirmed did not return after Set")
	}
}

func TestFakeReceipts_CtxCancel(t *testing.T) {
	r := NewFakeReceipts()
	hash := chain.TxHash{0x04}
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := r.WaitConfirmed(ctx, hash, 4)
		done <- err
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Errorf("WaitConfirmed err = %v, want Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitConfirmed did not return after cancel")
	}
}

func TestFakeReceipts_WaitCount(t *testing.T) {
	r := NewFakeReceipts()
	hash := chain.TxHash{0x05}
	// Outcomes are consumed-on-read, so each WaitConfirmed needs its own
	// pre-set outcome to return without blocking.
	r.Set(hash, &receipts.Receipt{Confirmed: true})
	_, _ = r.WaitConfirmed(context.Background(), hash, 4)
	r.Set(hash, &receipts.Receipt{Confirmed: true})
	_, _ = r.WaitConfirmed(context.Background(), hash, 4)
	if r.WaitCount != 2 {
		t.Errorf("WaitCount = %d, want 2", r.WaitCount)
	}
}

func TestFakeReceipts_OutcomeConsumedOnRead(t *testing.T) {
	r := NewFakeReceipts()
	hash := chain.TxHash{0x06}
	r.Set(hash, &receipts.Receipt{Confirmed: true})

	got1, err := r.WaitConfirmed(context.Background(), hash, 4)
	if err != nil || got1 == nil || !got1.Confirmed {
		t.Fatalf("first WaitConfirmed = (%v, %v)", got1, err)
	}

	// Second call must block until a new Set; verify with a short ctx.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = r.WaitConfirmed(ctx, hash, 4)
	if err != context.DeadlineExceeded {
		t.Errorf("second WaitConfirmed (no new Set) = %v, want DeadlineExceeded", err)
	}
}
