package chaintesting

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/ethereum/go-ethereum"
)

func TestFakeRPC_DefaultsAreSane(t *testing.T) {
	r := NewFakeRPC()
	ctx := context.Background()

	if id, err := r.ChainID(ctx); err != nil || id != 42161 {
		t.Errorf("ChainID = (%d, %v), want (42161, nil)", id, err)
	}
	if n, err := r.PendingNonceAt(ctx, chain.Address{}); err != nil || n != 0 {
		t.Errorf("PendingNonceAt = (%d, %v)", n, err)
	}
	if code, err := r.CodeAt(ctx, chain.Address{}, nil); err != nil || len(code) == 0 {
		t.Errorf("CodeAt default should be non-empty (preflight expects 'has code')")
	}
}

func TestFakeRPC_ConfigurableFunc(t *testing.T) {
	r := NewFakeRPC()
	r.ChainIDFunc = func(_ context.Context) (chain.ChainID, error) {
		return 1, nil
	}
	id, _ := r.ChainID(context.Background())
	if id != 1 {
		t.Errorf("ChainID with override = %d, want 1", id)
	}
}

func TestFakeRPC_InjectError(t *testing.T) {
	r := NewFakeRPC()
	want := errors.New("rpc-down")
	r.InjectError("ChainID", want)
	if _, err := r.ChainID(context.Background()); err != want {
		t.Errorf("first ChainID = %v, want %v", err, want)
	}
	// Second call recovers (only one error injected).
	if _, err := r.ChainID(context.Background()); err != nil {
		t.Errorf("second ChainID = %v, want nil", err)
	}
}

func TestFakeRPC_InjectErrorN(t *testing.T) {
	r := NewFakeRPC()
	want := errors.New("rpc-down")
	r.InjectErrorN("ChainID", want, 2)
	for i := 0; i < 2; i++ {
		if _, err := r.ChainID(context.Background()); err != want {
			t.Errorf("call %d = %v, want %v", i, err, want)
		}
	}
	if _, err := r.ChainID(context.Background()); err != nil {
		t.Errorf("after exhaust, err = %v, want nil", err)
	}
}

func TestFakeRPC_CallCount(t *testing.T) {
	r := NewFakeRPC()
	_, _ = r.ChainID(context.Background())
	_, _ = r.ChainID(context.Background())
	_, _ = r.PendingNonceAt(context.Background(), chain.Address{})
	if got := r.CallCount("ChainID"); got != 2 {
		t.Errorf("ChainID call count = %d, want 2", got)
	}
	if got := r.CallCount("PendingNonceAt"); got != 1 {
		t.Errorf("PendingNonceAt call count = %d, want 1", got)
	}
}

func TestFakeRPC_InjectLatency(t *testing.T) {
	r := NewFakeRPC()
	r.InjectLatency(50 * time.Millisecond)

	start := time.Now()
	_, _ = r.ChainID(context.Background())
	elapsed := time.Since(start)

	if elapsed < 40*time.Millisecond {
		t.Errorf("InjectLatency: call returned in %v, expected ~50ms", elapsed)
	}
}

func TestFakeRPC_LatencyHonorsCancel(t *testing.T) {
	r := NewFakeRPC()
	r.InjectLatency(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.ChainID(ctx)
	if err != context.Canceled {
		t.Errorf("ChainID with cancelled ctx = %v, want context.Canceled", err)
	}
}

func TestFakeRPC_BalanceAtDefault(t *testing.T) {
	r := NewFakeRPC()
	r.DefaultBalance = big.NewInt(1000)
	got, err := r.BalanceAt(context.Background(), chain.Address{}, nil)
	if err != nil || got.Int64() != 1000 {
		t.Errorf("BalanceAt = (%v, %v)", got, err)
	}
}

func TestFakeRPC_TransactionReceiptDefaultsToNotFound(t *testing.T) {
	r := NewFakeRPC()
	_, err := r.TransactionReceipt(context.Background(), chain.TxHash{})
	if err != ethereum.NotFound {
		t.Errorf("TransactionReceipt default = %v, want ethereum.NotFound", err)
	}
}

func TestFakeRPC_Close(t *testing.T) {
	r := NewFakeRPC()
	if err := r.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}
