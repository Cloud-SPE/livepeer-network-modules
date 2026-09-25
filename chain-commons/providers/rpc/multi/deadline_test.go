package multi

import (
	"context"
	"errors"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestAttemptDeadlineAndParentCancellation(t *testing.T) {
	for _, parentDeadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "failover", true: "parent_deadline"}[parentDeadline], func(t *testing.T) {
			var attempts atomic.Int32
			stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				attempts.Add(1)
				<-r.Context().Done()
			}))
			defer stalled.Close()
			backup := newFakeRPCServer(func(string) (string, error) { return `"0xa4b1"`, nil })
			defer backup.Close()
			p := defaultPolicy()
			p.MaxRetries = 1
			p.CallTimeout = 30 * time.Millisecond
			p.HealthProbeInterval = time.Hour
			m, err := Open(Options{URLs: []string{stalled.URL, backup.URL()}, Policy: p})
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if parentDeadline {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 10*time.Millisecond)
				defer stop()
			}
			start := time.Now()
			id, err := m.ChainID(ctx)
			if parentDeadline {
				if !errors.Is(err, context.DeadlineExceeded) || backup.Calls() != 0 {
					t.Fatalf("err=%v backup=%d", err, backup.Calls())
				}
				if m.Endpoints()[0].ConsecutiveFailures != 0 {
					t.Fatal("parent cancellation penalized endpoint")
				}
			} else {
				if err != nil || id != 42161 {
					t.Fatalf("id=%d err=%v", id, err)
				}
				if attempts.Load() != 2 || m.Endpoints()[0].ConsecutiveFailures != 2 {
					t.Fatalf("attempts=%d endpoints=%+v", attempts.Load(), m.Endpoints())
				}
			}
			if time.Since(start) > 500*time.Millisecond {
				t.Fatal("deadline did not bound RPC")
			}
		})
	}
}

func TestCancelledCallerDoesNotStartAttempt(t *testing.T) {
	server := newFakeRPCServer(func(string) (string, error) { return `"0xa4b1"`, nil })
	defer server.Close()
	m, err := Open(Options{URLs: []string{server.URL()}, Policy: defaultPolicy()})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = m.ChainID(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if server.Calls() != 0 {
		t.Fatal("cancelled call reached endpoint")
	}
}

func TestEveryWrapperHonorsAttemptTimeout(t *testing.T) {
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer stalled.Close()
	p := defaultPolicy()
	p.MaxRetries = 1
	p.CallTimeout = 5 * time.Millisecond
	p.CircuitBreakerThreshold = 1000
	p.HealthProbeInterval = time.Hour
	m, err := Open(Options{URLs: []string{stalled.URL, stalled.URL}, Policy: p})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	addr := chain.Address{1}
	hash := chain.TxHash{1}
	msg := ethereum.CallMsg{To: &addr}
	tx := types.NewTransaction(0, addr, big.NewInt(1), 21000, big.NewInt(1), nil)
	calls := map[string]func(context.Context) error{
		"CallContract":        func(ctx context.Context) error { _, e := m.CallContract(ctx, msg, nil); return e },
		"PendingCallContract": func(ctx context.Context) error { _, e := m.PendingCallContract(ctx, msg); return e },
		"CodeAt":              func(ctx context.Context) error { _, e := m.CodeAt(ctx, addr, nil); return e },
		"EstimateGas":         func(ctx context.Context) error { _, e := m.EstimateGas(ctx, msg); return e },
		"SendTransaction":     func(ctx context.Context) error { return m.SendTransaction(ctx, tx) },
		"TransactionByHash":   func(ctx context.Context) error { _, _, e := m.TransactionByHash(ctx, hash); return e },
		"TransactionReceipt":  func(ctx context.Context) error { _, e := m.TransactionReceipt(ctx, hash); return e },
		"BlockByNumber":       func(ctx context.Context) error { _, e := m.BlockByNumber(ctx, nil); return e },
		"HeaderByNumber":      func(ctx context.Context) error { _, e := m.HeaderByNumber(ctx, nil); return e },
		"FilterLogs":          func(ctx context.Context) error { _, e := m.FilterLogs(ctx, ethereum.FilterQuery{}); return e },
		"PendingNonceAt":      func(ctx context.Context) error { _, e := m.PendingNonceAt(ctx, addr); return e },
		"BalanceAt":           func(ctx context.Context) error { _, e := m.BalanceAt(ctx, addr, nil); return e },
		"SuggestGasPrice":     func(ctx context.Context) error { _, e := m.SuggestGasPrice(ctx); return e },
		"SuggestGasTipCap":    func(ctx context.Context) error { _, e := m.SuggestGasTipCap(ctx); return e },
		"ChainID":             func(ctx context.Context) error { _, e := m.ChainID(ctx); return e },
	}
	for name, call := range calls {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := call(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("got %v", err)
			}
			if ctx.Err() != nil {
				t.Fatal("attempt deadline was not applied; parent expired")
			}
		})
	}
}
func TestCancellationStopsBackoff(t *testing.T) {
	started := make(chan struct{}, 1)
	primary := newFakeRPCServer(func(string) (string, error) { started <- struct{}{}; return "", errors.New("temporarily down") })
	defer primary.Close()
	backup := newFakeRPCServer(func(string) (string, error) { return `"0xa4b1"`, nil })
	defer backup.Close()
	p := defaultPolicy()
	p.InitialBackoff = time.Hour
	p.MaxBackoff = time.Hour
	p.HealthProbeInterval = time.Hour
	m, err := Open(Options{URLs: []string{primary.URL(), backup.URL()}, Policy: p})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := m.ChainID(ctx); done <- err }()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("backoff ignored cancellation")
	}
	if primary.Calls() != 1 || backup.Calls() != 0 {
		t.Fatal("cancelled operation retried or failed over")
	}
}
