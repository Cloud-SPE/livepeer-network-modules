package sessionengine

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
)

// ---------------------------------------------------------------------------
// fakes

type debitCall struct {
	units int64
	seq   uint64
}

type fakePayment struct {
	mu           sync.Mutex
	debits       []debitCall
	debitSeqSeen map[uint64]int64 // daemon-side idempotency by seq
	failDebits   int              // fail the next N debit calls
	closed       int
	sufficient   bool
	balance      *big.Int
}

func newFakePayment() *fakePayment {
	return &fakePayment{debitSeqSeen: map[uint64]int64{}, sufficient: true, balance: big.NewInt(1000)}
}

func (f *fakePayment) GetTicketParams(context.Context, payment.GetTicketParamsRequest) (*payment.TicketParams, error) {
	return nil, errors.New("unused")
}
func (f *fakePayment) OpenSession(context.Context, payment.OpenSessionRequest) (*payment.OpenSessionResult, error) {
	return &payment.OpenSessionResult{}, nil
}
func (f *fakePayment) ProcessPayment(_ context.Context, req payment.ProcessPaymentRequest) (*payment.ProcessPaymentResult, error) {
	if len(req.PaymentBytes) == 0 {
		return nil, errors.New("empty payment")
	}
	return &payment.ProcessPaymentResult{Sender: []byte{0xAB}, Balance: f.balance}, nil
}
func (f *fakePayment) DebitBalance(_ context.Context, req payment.DebitBalanceRequest) (*big.Int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failDebits > 0 {
		f.failDebits--
		return nil, errors.New("transient daemon failure")
	}
	// Idempotent by seq, like the real daemon.
	if _, seen := f.debitSeqSeen[req.DebitSeq]; !seen {
		f.debitSeqSeen[req.DebitSeq] = req.WorkUnits
		f.debits = append(f.debits, debitCall{req.WorkUnits, req.DebitSeq})
	}
	return big.NewInt(0), nil
}
func (f *fakePayment) SufficientBalance(context.Context, payment.SufficientBalanceRequest) (*payment.SufficientBalanceResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &payment.SufficientBalanceResult{Sufficient: f.sufficient, Balance: f.balance}, nil
}
func (f *fakePayment) GetBalance(context.Context, []byte, string) (*big.Int, error) {
	return f.balance, nil
}
func (f *fakePayment) CloseSession(context.Context, []byte, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

func (f *fakePayment) totalDebited() int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var n int64
	for _, d := range f.debits {
		n += d.units
	}
	return n
}

type fakeRunner struct {
	mu         sync.Mutex
	created    int
	terminated []string
	gone       bool
	runtime    json.RawMessage
	failCreate bool
}

func validRuntime() json.RawMessage {
	return json.RawMessage(`{
		"schema": "sfu-room/v1",
		"public": {"url": "wss://sfu", "room": "rm_1"},
		"private": {"terminate_token": "rt_secret"},
		"grants": [{"id":"g1","operations":["participant-token-mint"],"secret":"gs_secret","expires_at":"2030-01-01T00:00:00Z"}]
	}`)
}

func (f *fakeRunner) CreateSession(_ context.Context, req RunnerCreateRequest) (*RunnerCreateResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failCreate {
		return nil, errors.New("runner create refused")
	}
	f.created++
	rt := f.runtime
	if rt == nil {
		rt = validRuntime()
	}
	return &RunnerCreateResult{RunnerSessionID: "rns_1", Runtime: rt}, nil
}
func (f *fakeRunner) QuerySession(context.Context, string) (*RunnerStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gone {
		return nil, ErrRunnerSessionGone
	}
	return &RunnerStatus{RunnerSessionID: "rns_1", State: "active"}, nil
}
func (f *fakeRunner) TerminateSession(_ context.Context, id, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terminated = append(f.terminated, reason)
	return nil
}

// ---------------------------------------------------------------------------
// harness

type harness struct {
	engine  *Engine
	store   *sessionstore.Store
	pay     *fakePayment
	runner  *fakeRunner
	spec    *OfferingSpec
	nowVal  time.Time
	nowMu   sync.Mutex
	release []string
}

func (h *harness) now() time.Time {
	h.nowMu.Lock()
	defer h.nowMu.Unlock()
	return h.nowVal
}

func (h *harness) advance(d time.Duration) {
	h.nowMu.Lock()
	defer h.nowMu.Unlock()
	h.nowVal = h.nowVal.Add(d)
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	key := make([]byte, sessionstore.KeySize)
	st, err := sessionstore.Open(filepath.Join(t.TempDir(), "s.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	h := &harness{
		store:  st,
		pay:    newFakePayment(),
		runner: &fakeRunner{},
		nowVal: time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC),
	}
	h.spec = &OfferingSpec{
		Capability:          "livepeer:meet/sfu-room",
		Offering:            "default",
		BackendRef:          "b1",
		WorkUnit:            "participant_minutes",
		PricePerWorkUnitWei: big.NewInt(10),
		DescriptorSchema:    "sfu-room/v1",
		HeartbeatInterval:   10 * time.Second,
		MissedThreshold:     3,
		MinRunwayUnits:      5,
	}
	eng, err := New(Config{
		Store:           st,
		Payment:         h.pay,
		Runner:          func(string) RunnerClient { return h.runner },
		Specs:           func(string) *OfferingSpec { return h.spec },
		Callback:        CallbackConfig{BaseURL: "https://broker.example.com"},
		ReleaseCapacity: func(ref string) { h.release = append(h.release, ref) },
		Now:             h.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.engine = eng
	return h
}

func (h *harness) open(t *testing.T) *OpenResult {
	t.Helper()
	res, err := h.engine.Open(context.Background(), OpenRequest{
		RequestID:        "req-1",
		GatewaySessionID: "gws-1",
		SessionParams:    json.RawMessage(`{"room_hint":"standup"}`),
		PaymentBytes:     []byte{1, 2, 3},
		Spec:             h.spec,
		CapacityRef:      "cap-slot-1",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return res
}

func usageEvent(id string, seq uint64, total uint64) Event {
	return Event{
		EventID: id, Sequence: seq, EventType: "session.usage.tick",
		UsageUnit: "participant_minutes", UsageTot: &total,
	}
}

// ---------------------------------------------------------------------------
// tests

func TestOpenHappyPath(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	if res.Credential == "" || len(res.Grants) != 1 || res.Grants[0].Secret != "gs_secret" {
		t.Fatalf("open must deliver credential and grants once: %+v", res)
	}
	rec, err := h.store.Get(res.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !sessionstore.VerifySecret(rec.CredentialHash, res.Credential) {
		t.Fatal("stored credential hash does not verify")
	}
	if rec.Grants[0].ID != "g1" || len(rec.Grants[0].SecretHash) == 0 {
		t.Fatal("grant audit missing")
	}
	if rec.WorkID == "" || rec.RunnerSessionID != "rns_1" {
		t.Fatalf("binding incomplete: %+v", rec)
	}
}

func TestOpenIdempotentReplay(t *testing.T) {
	h := newHarness(t)
	first := h.open(t)
	replay, err := h.engine.Open(context.Background(), OpenRequest{
		RequestID: "req-1", PaymentBytes: []byte{1}, Spec: h.spec,
	})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.Replayed || replay.SessionID != first.SessionID || replay.WorkID != first.WorkID {
		t.Fatalf("replay must return the original session: %+v", replay)
	}
	if replay.Credential != "" || len(replay.Grants) != 0 {
		t.Fatal("replay must never re-deliver credential or grants")
	}
	if h.runner.created != 1 {
		t.Fatalf("replay created a second runner session: %d", h.runner.created)
	}
}

func TestOpenFailsClosedOnBadDescriptor(t *testing.T) {
	h := newHarness(t)
	h.runner.runtime = json.RawMessage(`{"schema":"sfu-room/v1","public":{},"surprise":{}}`)
	_, err := h.engine.Open(context.Background(), OpenRequest{
		RequestID: "req-x", PaymentBytes: []byte{1}, Spec: h.spec, CapacityRef: "slot",
	})
	if err == nil {
		t.Fatal("expected descriptor rejection")
	}
	if len(h.runner.terminated) != 1 {
		t.Fatal("runner session not terminated on fail-closed open")
	}
	if h.pay.closed != 1 {
		t.Fatal("payment session not closed on fail-closed open")
	}
	if len(h.release) != 1 || h.release[0] != "slot" {
		t.Fatal("capacity not released on fail-closed open")
	}
}

func TestExactlyOnceDebitUnderRetry(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	ctx := context.Background()

	// First tick debits 5.
	if _, err := h.engine.ProcessEvent(ctx, res.SessionID, usageEvent("evt_1", 1, 5)); err != nil {
		t.Fatalf("tick1: %v", err)
	}
	// Transient debit failure: nothing advances.
	h.pay.failDebits = 1
	_, err := h.engine.ProcessEvent(ctx, res.SessionID, usageEvent("evt_2", 2, 12))
	var re *RetryableError
	if !errors.As(err, &re) {
		t.Fatalf("expected retryable error, got %v", err)
	}
	rec, _ := h.store.Get(res.SessionID)
	if rec.LastSequence != 1 || rec.ClaimedTotal != 5 || rec.DebitSeq != 1 {
		t.Fatalf("failed debit advanced state: %+v", rec)
	}
	// Runner retries the same event: exactly one more debit of 7.
	if _, err := h.engine.ProcessEvent(ctx, res.SessionID, usageEvent("evt_2", 2, 12)); err != nil {
		t.Fatalf("retry: %v", err)
	}
	// Duplicate delivery of a committed event: no-op.
	out, err := h.engine.ProcessEvent(ctx, res.SessionID, usageEvent("evt_2", 2, 12))
	if err != nil || !out.Duplicate {
		t.Fatalf("expected duplicate outcome, got %+v %v", out, err)
	}
	if got := h.pay.totalDebited(); got != 12 {
		t.Fatalf("total debited %d; want exactly 12 (never 5, never 19)", got)
	}
	rec, _ = h.store.Get(res.SessionID)
	if rec.DebitedTotal != 12 || rec.DebitSeq != 2 {
		t.Fatalf("commit state wrong: %+v", rec)
	}
}

func TestUnitMismatchAdvancesNothing(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	total := uint64(9)
	_, err := h.engine.ProcessEvent(context.Background(), res.SessionID, Event{
		EventID: "evt_1", Sequence: 1, EventType: "session.usage.tick",
		UsageUnit: "frames", UsageTot: &total,
	})
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != "usage_unit_mismatch" {
		t.Fatalf("expected usage_unit_mismatch, got %v", err)
	}
	rec, _ := h.store.Get(res.SessionID)
	if rec.LastSequence != 0 || rec.ClaimedTotal != 0 {
		t.Fatal("unit mismatch advanced idempotency or totals")
	}
	// A subsequent correct event with the same sequence still debits fully.
	if _, err := h.engine.ProcessEvent(context.Background(), res.SessionID, usageEvent("evt_1", 1, 9)); err != nil {
		t.Fatalf("correct retry: %v", err)
	}
	if h.pay.totalDebited() != 9 {
		t.Fatalf("debited %d, want 9", h.pay.totalDebited())
	}
}

func TestEmptyEventIDAndRegressionRejected(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	ctx := context.Background()
	if _, err := h.engine.ProcessEvent(ctx, res.SessionID, usageEvent("", 1, 5)); err == nil {
		t.Fatal("empty event id accepted")
	}
	if _, err := h.engine.ProcessEvent(ctx, res.SessionID, usageEvent("evt_1", 1, 10)); err != nil {
		t.Fatal(err)
	}
	_, err := h.engine.ProcessEvent(ctx, res.SessionID, usageEvent("evt_2", 2, 3))
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != "usage_regression" {
		t.Fatalf("expected usage_regression, got %v", err)
	}
}

func TestRunnerEndedRunsTerminalPath(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	total := uint64(20)
	out, err := h.engine.ProcessEvent(context.Background(), res.SessionID, Event{
		EventID: "evt_9", Sequence: 1, EventType: "session.ended",
		UsageUnit: "participant_minutes", UsageTot: &total,
	})
	if err != nil || !out.Terminal {
		t.Fatalf("ended event: %+v %v", out, err)
	}
	rec, _ := h.store.Get(res.SessionID)
	if !rec.Terminal() || rec.CloseReason != ReasonRunnerEnded || !rec.PaymentClosed {
		t.Fatalf("terminal path incomplete: %+v", rec)
	}
	if h.pay.totalDebited() != 20 {
		t.Fatalf("final usage not debited: %d", h.pay.totalDebited())
	}
	if len(h.runner.terminated) == 0 {
		t.Fatal("runner not terminated")
	}
}

func TestInsufficientBalanceForcesWinddown(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	h.pay.sufficient = false
	out, err := h.engine.ProcessEvent(context.Background(), res.SessionID, usageEvent("evt_1", 1, 5))
	if err != nil {
		t.Fatal(err)
	}
	if !out.Insufficient || !out.Terminal {
		t.Fatalf("expected insufficient winddown: %+v", out)
	}
	rec, _ := h.store.Get(res.SessionID)
	if rec.CloseReason != ReasonInsufficient {
		t.Fatalf("close reason %q", rec.CloseReason)
	}
}

func TestSweepHeartbeatLost(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	// Keep the lease far away so heartbeat is the trigger under test.
	_ = h.store.Update(res.SessionID, func(r *sessionstore.Record) error {
		r.LeaseExpiresAt = h.now().Add(time.Hour)
		return nil
	})
	h.advance(31 * time.Second) // > interval(10s) * threshold(3)
	h.engine.Sweep(context.Background())
	rec, _ := h.store.Get(res.SessionID)
	if rec.CloseReason != ReasonHeartbeatLost {
		t.Fatalf("expected heartbeat_lost, got %q (state %s)", rec.CloseReason, rec.State)
	}
	if !rec.PaymentClosed || len(h.runner.terminated) == 0 {
		t.Fatal("winddown incomplete on heartbeat loss")
	}
	// Idempotent: sweeping again changes nothing.
	h.engine.Sweep(context.Background())
	rec2, _ := h.store.Get(res.SessionID)
	if rec2.EndedAt != rec.EndedAt {
		t.Fatal("second sweep mutated terminal record")
	}
}

func TestSweepLeaseExpiryRespectsGrace(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	lease := h.now().Add(20 * time.Second)
	_ = h.store.Update(res.SessionID, func(r *sessionstore.Record) error {
		r.LeaseExpiresAt = lease
		return nil
	})
	// Just past expiry but inside the one-heartbeat grace: still active.
	h.advance(25 * time.Second)
	// Keep heartbeat fresh so lease is the trigger under test.
	_ = h.store.Update(res.SessionID, func(r *sessionstore.Record) error {
		r.LastEventAt = h.now()
		return nil
	})
	h.engine.Sweep(context.Background())
	rec, _ := h.store.Get(res.SessionID)
	if rec.Terminal() {
		t.Fatal("winddown fired inside the grace window")
	}
	// Past expiry + grace: winddown with lease_expired.
	h.advance(10 * time.Second)
	_ = h.store.Update(res.SessionID, func(r *sessionstore.Record) error {
		r.LastEventAt = h.now()
		return nil
	})
	h.engine.Sweep(context.Background())
	rec, _ = h.store.Get(res.SessionID)
	if rec.CloseReason != ReasonLeaseExpired {
		t.Fatalf("expected lease_expired, got %q", rec.CloseReason)
	}
}

func TestRecoverRebindsOrTerminates(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)

	// Runner still holds the session: rebind (record untouched).
	h.engine.Recover(context.Background())
	rec, _ := h.store.Get(res.SessionID)
	if rec.Terminal() {
		t.Fatal("rebind wrongly terminated the session")
	}
	if rec.WorkID != res.WorkID {
		t.Fatal("recovery changed work_id")
	}

	// Runner lost it: explicit terminal outcome.
	h.runner.gone = true
	h.engine.Recover(context.Background())
	rec, _ = h.store.Get(res.SessionID)
	if rec.State != sessionstore.StateFailed || rec.CloseReason != ReasonRecoveryFailed {
		t.Fatalf("expected recovery_failed terminal, got %+v", rec)
	}
	if !rec.PaymentClosed {
		t.Fatal("payment left open after recovery termination")
	}
}

func TestEndIsIdempotent(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	first, err := h.engine.End(context.Background(), res.SessionID, "")
	if err != nil || first.CloseReason != ReasonGatewayClose {
		t.Fatalf("end: %+v %v", first, err)
	}
	second, err := h.engine.End(context.Background(), res.SessionID, "other_reason")
	if err != nil {
		t.Fatal(err)
	}
	if second.CloseReason != ReasonGatewayClose || second.EndedAt != first.EndedAt {
		t.Fatal("repeat end mutated the terminal record")
	}
	if h.pay.closed != 1 {
		t.Fatalf("payment closed %d times", h.pay.closed)
	}
}

func TestTopUpExtendsLeaseAndRefusesTerminal(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	before, _ := h.store.Get(res.SessionID)
	h.pay.balance = big.NewInt(5000) // more funding arrived
	out, err := h.engine.TopUp(context.Background(), res.SessionID, []byte{9})
	if err != nil {
		t.Fatalf("topup: %v", err)
	}
	if !out.Lease.After(before.LeaseExpiresAt) {
		t.Fatalf("top-up did not extend lease: %v -> %v", before.LeaseExpiresAt, out.Lease)
	}
	rec, _ := h.store.Get(res.SessionID)
	if !rec.LeaseExpiresAt.Equal(out.Lease) {
		t.Fatal("lease not persisted")
	}
	// Terminal sessions refuse refill with a stable code.
	if _, err := h.engine.End(context.Background(), res.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	_, err = h.engine.TopUp(context.Background(), res.SessionID, []byte{9})
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != "refill_refused" {
		t.Fatalf("expected refill_refused, got %v", err)
	}
}
