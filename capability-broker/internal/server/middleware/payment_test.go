package middleware

import (
	"context"
	"encoding/base64"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/observability"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/receipts"
	"github.com/prometheus/client_golang/prometheus/testutil"
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
	r.Header.Set(livepeerheader.Protocol, "paid-session/v1")
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

type stubReceiptSink struct {
	items []receipts.WorkReceipt
	err   error
}

func (s *stubReceiptSink) UpsertWorkReceipt(_ context.Context, receipt receipts.WorkReceipt) error {
	s.items = append(s.items, receipt)
	return s.err
}

type invalidRecipientRandClient struct {
	*payment.Mock
}

func (c *invalidRecipientRandClient) ProcessPayment(_ context.Context, req payment.ProcessPaymentRequest) (*payment.ProcessPaymentResult, error) {
	if req.WorkID == "" {
		return nil, errors.New("work_id is empty")
	}
	return &payment.ProcessPaymentResult{
		Sender:            []byte("01234567890123456789"),
		CreditedEV:        new(big.Int),
		Balance:           new(big.Int),
		TicketsRejected:   1,
		DominantRejection: payment.PaymentRejectionReasonInvalidRecipientRand,
		TicketStatus: []payment.TicketStatus{{
			SenderNonce:     1,
			RejectionReason: payment.PaymentRejectionReasonInvalidRecipientRand,
			CreditedEV:      new(big.Int),
		}},
	}, nil
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
	}, nil, nil)

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
	}, nil, nil)

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
	}, nil, nil)

	lc := &fakeLiveCounter{}
	handlerCtxObserved := make(chan struct{})
	var handlerCancelObserved atomic.Bool
	var wg sync.WaitGroup

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := SessionStateFromContext(r.Context())
		state.SetLiveCounter(lc)
		// Empty the session explicitly: with price=1 and min=100,
		// SufficientBalance returns false on the first tick. Stated
		// rather than inherited from the mock, which now credits.
		if err := mock.SetBalance(RequestIDFromContext(r.Context()), new(big.Int)); err != nil {
			t.Errorf("SetBalance: %v", err)
		}
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
	}, nil, nil)

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
	}, nil, nil)

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

// TestPayment_InvalidRecipientRandReturnsRecipientRotated: the payee
// rotating its rand is not a generic payment failure. It has a
// mechanical remedy — re-fetch params, re-mint, retry — so the gateway
// gets a code it can act on rather than a message to match.
func TestPayment_InvalidRecipientRandReturnsRecipientRotated(t *testing.T) {
	t.Parallel()

	client := &invalidRecipientRandClient{Mock: payment.NewMock()}
	mw := Payment(client, stubLookup, InterimDebitConfig{Interval: 0}, nil, nil)

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := makePaidRequest("wid-invalid-rand")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if called {
		t.Fatal("handler should not run when ticket params are invalidated")
	}
	if rec.Code != http.StatusConflict {
		t.Fatalf("status: got %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(livepeerheader.Error); got != livepeerheader.ErrRecipientRotated {
		t.Fatalf("Livepeer-Error = %q; want %q", got, livepeerheader.ErrRecipientRotated)
	}
	if body := rec.Body.String(); body == "" || !contains(body, "rotated") {
		t.Fatalf("body = %q; want the rotation named", body)
	}
}

func contains(s, needle string) bool {
	return len(needle) == 0 || (len(s) >= len(needle) && func() bool {
		for i := 0; i+len(needle) <= len(s); i++ {
			if s[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	}())
}

func TestPayment_EmitsFinalReceiptWhenMetaPresent(t *testing.T) {
	t.Parallel()
	mock := payment.NewMock()
	sink := &stubReceiptSink{}
	before := testutil.ToFloat64(observability.TestWorkReceiptEmitCounter("final", "success"))

	mw := Payment(mock, stubLookup, InterimDebitConfig{Interval: 0}, sink, nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := SessionStateFromContext(r.Context())
		state.SetReceiptMeta(ReceiptMeta{
			WorkID:           "wid-receipt",
			RequestID:        "req-receipt",
			CapabilityID:     "cap",
			OfferingID:       "off",
			MemberEthAddress: "0xabc",
			BackendID:        "backend-a",
		})
		w.Header().Set(livepeerheader.WorkUnits, "42")
		w.WriteHeader(http.StatusOK)
	}))

	req := makePaidRequest("wid-receipt")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if len(sink.items) != 1 {
		t.Fatalf("receipt count = %d, want 1", len(sink.items))
	}
	got := sink.items[0]
	if got.ID != "wid-receipt" || got.Status != "final" || got.ActualUnits != 42 {
		t.Fatalf("receipt = %#v", got)
	}
	if got.GatewayRevenueWei != "42" {
		t.Fatalf("gateway revenue = %q, want 42", got.GatewayRevenueWei)
	}
	after := testutil.ToFloat64(observability.TestWorkReceiptEmitCounter("final", "success"))
	if after != before+1 {
		t.Fatalf("final receipt emit delta = %v; want 1", after-before)
	}
}

func TestPayment_EmitsFinalReceiptErrorMetricWhenSinkFails(t *testing.T) {
	t.Parallel()
	mock := payment.NewMock()
	sink := &stubReceiptSink{err: errors.New("boom")}
	before := testutil.ToFloat64(observability.TestWorkReceiptEmitCounter("final", "error"))

	mw := Payment(mock, stubLookup, InterimDebitConfig{Interval: 0}, sink, nil)
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := SessionStateFromContext(r.Context())
		state.SetReceiptMeta(ReceiptMeta{
			WorkID:           "wid-receipt-error",
			RequestID:        "req-receipt-error",
			CapabilityID:     "cap",
			OfferingID:       "off",
			MemberEthAddress: "0xabc",
			BackendID:        "backend-a",
		})
		w.Header().Set(livepeerheader.WorkUnits, "42")
		w.WriteHeader(http.StatusOK)
	}))

	req := makePaidRequest("wid-receipt-error")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if len(sink.items) != 1 {
		t.Fatalf("receipt count = %d, want 1", len(sink.items))
	}
	after := testutil.ToFloat64(observability.TestWorkReceiptEmitCounter("final", "error"))
	if after != before+1 {
		t.Fatalf("final receipt error emit delta = %v; want 1", after-before)
	}
}

// TestPayment_RefusesWorkAgainstAnUnfundedSession is what the mainnet
// probe exposed: a unary job ran the backend and returned results
// against a session with zero balance, reporting success at every layer.
// The interim ticker guards long-running work and is a no-op here, so
// nothing checked before the backend ran.
func TestPayment_RefusesWorkAgainstAnUnfundedSession(t *testing.T) {
	t.Parallel()
	mock := payment.NewMock()
	mw := Payment(mock, stubLookup, InterimDebitConfig{Interval: 0}, nil, nil)

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	// A payment that credits nothing — a valid ticket whose expected
	// value rounded away, which is what the mainnet run produced.
	mock.SetCreditPerPayment(new(big.Int))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, makePaidRequest("wid-unfunded"))

	if called {
		t.Fatal("backend ran for a session that cannot pay for one unit of work")
	}
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d; want 402", rec.Code)
	}
	if got := rec.Header().Get(livepeerheader.Error); got != livepeerheader.ErrInsufficientBalance {
		t.Fatalf("Livepeer-Error = %q; want %q", got, livepeerheader.ErrInsufficientBalance)
	}
}

// TestPayment_FundedSessionStillServes: the check must not refuse work a
// gateway has actually paid for.
func TestPayment_FundedSessionStillServes(t *testing.T) {
	t.Parallel()
	mock := payment.NewMock()
	mw := Payment(mock, stubLookup, InterimDebitConfig{Interval: 0}, nil, nil)

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set(livepeerheader.WorkUnits, "42")
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, makePaidRequest("wid-funded"))

	if !called {
		t.Fatalf("funded session was refused: status %d body %s", rec.Code, rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
}
