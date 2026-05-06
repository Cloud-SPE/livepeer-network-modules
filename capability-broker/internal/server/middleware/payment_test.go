package middleware

import (
	"context"
	"encoding/base64"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/payment"
)

// fakeLiveCounter is a goroutine-safe LiveCounter for middleware tests.
type fakeLiveCounter struct {
	v atomic.Uint64
}

func (f *fakeLiveCounter) CurrentUnits() uint64 { return f.v.Load() }
func (f *fakeLiveCounter) Add(n uint64)         { f.v.Add(n) }

// makePaidRequest constructs a request with the standard Livepeer-* headers
// the Payment middleware requires. workID is fixed so the daemon can
// look up the session.
func makePaidRequest(workID string) *http.Request {
	r := httptest.NewRequest("POST", "/v1/cap", nil)
	r.Header.Set(livepeerheader.Capability, "cap")
	r.Header.Set(livepeerheader.Offering, "off")
	r.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString([]byte("dummy-payment")))
	r.Header.Set(livepeerheader.SpecVersion, "0.1")
	r.Header.Set(livepeerheader.Mode, "ws-realtime@v0")
	// The Payment middleware reads RequestIDFromContext for work_id; the
	// RequestID middleware would normally set this. Inline the same
	// behavior for the test path.
	ctx := context.WithValue(r.Context(), requestIDKey, workID)
	return r.WithContext(ctx)
}

// stubLookup always returns a canned spec for any (capability, offering).
func stubLookup(cap, off string) (CapabilitySpec, bool) {
	return CapabilitySpec{
		WorkUnit:            "bytes",
		PricePerWorkUnitWei: big.NewInt(1),
	}, true
}

// TestPayment_TickerDisabledFallback documents the locked decision #6:
// `--interim-debit-interval=0` reverts to the v0.2 single-debit path.
// No SufficientBalance is invoked; one DebitBalance(seq=1) is issued at
// handler completion.
func TestPayment_TickerDisabledFallback(t *testing.T) {
	t.Parallel()
	mock := payment.NewMock()

	mw := Payment(mock, stubLookup, InterimDebitConfig{
		Interval: 0, // disabled
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(livepeerheader.WorkUnits, "42")
		w.WriteHeader(http.StatusOK)
	}))

	req := makePaidRequest("wid-disabled")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	sessions := mock.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if !s.Closed {
		t.Errorf("session should be closed at end of request")
	}
	if len(s.Debits) != 1 {
		t.Errorf("expected single debit (v0.2 path), got %d: %v", len(s.Debits), s.Debits)
	}
	if s.Debits[0] != 42 {
		t.Errorf("debit units: got %d, want 42", s.Debits[0])
	}
}

// TestPayment_TickerHappyPath drives the ticker with a LiveCounter that
// the handler increments over multiple ticks, then closes. Plan 0015
// §3.1 lifecycle: ≥2 interim debits + a final flush that completes
// the session.
func TestPayment_TickerHappyPath(t *testing.T) {
	t.Parallel()
	mock := payment.NewMock()

	mw := Payment(mock, stubLookup, InterimDebitConfig{
		Interval:       30 * time.Millisecond,
		MinRunwayUnits: 0, // disable SufficientBalance for this fixture
	})

	lc := &fakeLiveCounter{}
	handlerStart := make(chan struct{})
	handlerDone := make(chan struct{})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := SessionStateFromContext(r.Context())
		if state == nil {
			t.Errorf("SessionState missing from request context")
			return
		}
		state.SetLiveCounter(lc)

		close(handlerStart)
		// Drive the counter across at least 4 tick intervals.
		for i := 0; i < 4; i++ {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(35 * time.Millisecond):
			}
			lc.Add(50) // 50 units per slice
		}
		close(handlerDone)
		w.WriteHeader(http.StatusOK)
	}))

	req := makePaidRequest("wid-happy")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	<-handlerStart
	<-handlerDone

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	sessions := mock.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if !s.Closed {
		t.Errorf("session should be closed")
	}
	if len(s.Debits) < 2 {
		t.Errorf("expected ≥2 debits (interim + final), got %d: %v", len(s.Debits), s.Debits)
	}
	// Sum of debits must equal final LiveCounter value (200).
	var sum int64
	for _, d := range s.Debits {
		sum += d
	}
	if sum != 200 {
		t.Errorf("sum of debits: got %d, want 200 (final LiveCounter value); debits=%v", sum, s.Debits)
	}
}

// TestPayment_InsufficientBalanceTermination drives the ticker against
// a pre-loaded session whose price-times-min-runway exceeds balance.
// The middleware MUST cancel the handler context and exit. Plan 0015
// §6.2 termination semantics.
func TestPayment_InsufficientBalanceTermination(t *testing.T) {
	t.Parallel()
	mock := payment.NewMock()

	mw := Payment(mock, stubLookup, InterimDebitConfig{
		Interval:            20 * time.Millisecond,
		MinRunwayUnits:      100,
		GraceOnInsufficient: 0,
	})

	lc := &fakeLiveCounter{}
	handlerCtxObserved := make(chan struct{})
	var handlerCancelObserved atomic.Bool
	var wg sync.WaitGroup

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := SessionStateFromContext(r.Context())
		state.SetLiveCounter(lc)
		// Don't credit balance; mock starts at 0 → SufficientBalance
		// (price=1, min=100) returns false on first tick.
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-r.Context().Done()
			handlerCancelObserved.Store(true)
			close(handlerCtxObserved)
		}()
		// Block until ctx cancels (i.e. ticker terminated us).
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
			t.Errorf("handler not terminated within 2s; ticker did not cancel context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := makePaidRequest("wid-insuff")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	wg.Wait()
	if !handlerCancelObserved.Load() {
		t.Fatalf("handler did not observe context cancellation; ticker termination broken")
	}
	select {
	case <-handlerCtxObserved:
	default:
		t.Fatal("handler-side cancellation channel never closed")
	}
}

// TestPayment_InsufficientBalanceWithRunwayDoesNotTerminate verifies
// that when the session has enough balance, the ticker keeps running
// and does not cancel the handler.
func TestPayment_InsufficientBalanceWithRunwayDoesNotTerminate(t *testing.T) {
	t.Parallel()
	mock := payment.NewMock()

	mw := Payment(mock, stubLookup, InterimDebitConfig{
		Interval:       20 * time.Millisecond,
		MinRunwayUnits: 10, // price=1 × 10 = 10 wei runway
	})

	lc := &fakeLiveCounter{}
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := SessionStateFromContext(r.Context())
		state.SetLiveCounter(lc)
		// Seed sufficient balance (1000 wei covers 10 × 1 wei runway).
		if err := mock.CreditBalance(RequestIDFromContext(r.Context()), big.NewInt(1000)); err != nil {
			t.Errorf("CreditBalance: %v", err)
		}
		// Run for a few ticks; do nothing; expect the ticker to keep
		// the handler context alive.
		select {
		case <-r.Context().Done():
			t.Errorf("context cancelled despite sufficient runway: %v", r.Context().Err())
		case <-time.After(80 * time.Millisecond):
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := makePaidRequest("wid-suff")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
}

// TestPayment_NoLiveCounterSkipsTicks documents that without a published
// LiveCounter (HTTP-family modes), the ticker fires no debits even
// when enabled. The post-handler path falls through to the v0.2
// single-debit using the Livepeer-Work-Units header.
func TestPayment_NoLiveCounterSkipsTicks(t *testing.T) {
	t.Parallel()
	mock := payment.NewMock()
	mw := Payment(mock, stubLookup, InterimDebitConfig{
		Interval:       10 * time.Millisecond,
		MinRunwayUnits: 0,
	})

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Do NOT set LiveCounter. Sleep long enough for ≥3 ticks.
		time.Sleep(50 * time.Millisecond)
		w.Header().Set(livepeerheader.WorkUnits, "7")
		w.WriteHeader(http.StatusOK)
	}))

	req := makePaidRequest("wid-no-live")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	sessions := mock.Sessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if len(s.Debits) != 1 {
		t.Errorf("expected exactly 1 debit (no interim), got %d: %v", len(s.Debits), s.Debits)
	}
	if s.Debits[0] != 7 {
		t.Errorf("debit units: got %d, want 7", s.Debits[0])
	}
}
