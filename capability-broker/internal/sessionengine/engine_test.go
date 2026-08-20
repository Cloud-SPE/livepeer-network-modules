package sessionengine

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
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
	openCalls    int
	sessionGone  bool
	workIDs      []string
	openPerUnits []uint64
	ticketCount  int32 // tickets in the batch ProcessPayment reports
	ticketsBad   int32 // how many of them were rejected
	rejectReason payment.PaymentRejectionReason
}

func newFakePayment() *fakePayment {
	return &fakePayment{debitSeqSeen: map[uint64]int64{}, sufficient: true, balance: big.NewInt(1000)}
}

func (f *fakePayment) GetTicketParams(context.Context, payment.GetTicketParamsRequest) (*payment.TicketParams, error) {
	return nil, errors.New("unused")
}
func (f *fakePayment) OpenSession(_ context.Context, req payment.OpenSessionRequest) (*payment.OpenSessionResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.openCalls++
	f.workIDs = append(f.workIDs, req.WorkID)
	f.openPerUnits = append(f.openPerUnits, req.PerUnits)
	already := !f.sessionGone
	f.sessionGone = false // idempotent open (re-)establishes the session
	return &payment.OpenSessionResult{AlreadyOpen: already}, nil
}
func (f *fakePayment) ProcessPayment(_ context.Context, req payment.ProcessPaymentRequest) (*payment.ProcessPaymentResult, error) {
	if len(req.PaymentBytes) == 0 {
		return nil, errors.New("empty payment")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workIDs = append(f.workIDs, req.WorkID)
	res := &payment.ProcessPaymentResult{Sender: []byte{0xAB}, Balance: f.balance}
	// The daemon reports ticket outcomes in the result, not as an error.
	for i := int32(0); i < f.ticketCount; i++ {
		st := payment.TicketStatus{SenderNonce: uint32(i)}
		if i < f.ticketsBad {
			st.RejectionReason = f.rejectReason
		}
		res.TicketStatus = append(res.TicketStatus, st)
	}
	res.TicketsRejected = f.ticketsBad
	res.DominantRejection = f.rejectReason
	return res, nil
}
func (f *fakePayment) DebitBalance(_ context.Context, req payment.DebitBalanceRequest) (*big.Int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sessionGone {
		return nil, errors.New("session not found")
	}
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
	out, err := h.engine.TopUp(context.Background(), res.SessionID, "topup-1", []byte{9})
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
	// Terminal sessions refuse refill with a stable code. A fresh
	// request id, because a replay of the one above is answered from the
	// record and never reaches the terminal check.
	if _, err := h.engine.End(context.Background(), res.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	_, err = h.engine.TopUp(context.Background(), res.SessionID, "topup-2", []byte{9})
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != "refill_refused" {
		t.Fatalf("expected refill_refused, got %v", err)
	}
}

// TestRecoverRebindReassertsPaymentSession covers the healthy branch:
// the payment layer still holds the session (AlreadyOpen), so rebind
// re-asserts it as a no-op and usage keeps flowing on the same work_id.
func TestRecoverRebindReassertsPaymentSession(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	ctx := context.Background()

	h.pay.mu.Lock()
	h.pay.openCalls = 0
	h.pay.mu.Unlock()

	h.engine.Recover(ctx)

	h.pay.mu.Lock()
	opens := h.pay.openCalls
	h.pay.mu.Unlock()
	if opens == 0 {
		t.Fatal("rebind did not re-assert the payment session")
	}
	rec, _ := h.store.Get(res.SessionID)
	if rec.Terminal() {
		t.Fatalf("healthy rebind wound the session down: %s", rec.CloseReason)
	}
	if _, err := h.engine.ProcessEvent(ctx, res.SessionID, usageEvent("evt_after", 1, 4)); err != nil {
		t.Fatalf("post-rebind event: %v", err)
	}
	if h.pay.totalDebited() != 4 {
		t.Fatalf("debited %d, want 4", h.pay.totalDebited())
	}
}

// TestRecoverFailsClosedWhenPaymentSessionLost covers the branch the
// conformance suite exposed: the runner still holds the session but the
// payment layer lost it (OpenSession reports it was NOT already open).
// The session cannot be billed, so the broker must take the explicit
// terminal outcome rather than serve unmetered work.
func TestRecoverFailsClosedWhenPaymentSessionLost(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	ctx := context.Background()

	h.pay.mu.Lock()
	h.pay.sessionGone = true // payment daemon restarted and lost its ledger
	h.pay.mu.Unlock()

	h.engine.Recover(ctx)

	rec, _ := h.store.Get(res.SessionID)
	if !rec.Terminal() || rec.CloseReason != ReasonRecoveryFailed {
		t.Fatalf("want recovery_failed terminal, got state=%s reason=%q", rec.State, rec.CloseReason)
	}
	if !rec.PaymentClosed {
		t.Fatal("payment left open after fail-closed recovery")
	}
	if len(h.runner.terminated) == 0 {
		t.Fatal("runner left serving after fail-closed recovery")
	}
}

// TestSweepHeartbeatWinsOverLease pins the precedence decision: when a
// session is past both its lease and its heartbeat threshold, the stable
// reason is heartbeat_lost — a dead runner is the more specific fact and
// sends the operator to the runner rather than to funding.
func TestSweepHeartbeatWinsOverLease(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	_ = h.store.Update(res.SessionID, func(r *sessionstore.Record) error {
		r.LeaseExpiresAt = h.now().Add(-time.Hour) // long expired
		return nil
	})
	h.advance(31 * time.Second) // also past interval(10s) * threshold(3)
	h.engine.Sweep(context.Background())
	rec, _ := h.store.Get(res.SessionID)
	if rec.CloseReason != ReasonHeartbeatLost {
		t.Fatalf("close reason %q, want heartbeat_lost (lease was also expired)", rec.CloseReason)
	}
}

// TestTopUpRefusedOnBoundedOffering pins offering-axes §3: a bounded
// offering rejects top-up after open, with the stable refill_refused
// code.
func TestTopUpRefusedOnBoundedOffering(t *testing.T) {
	h := newHarness(t)
	h.spec.Refill = "bounded"
	res := h.open(t)
	_, err := h.engine.TopUp(context.Background(), res.SessionID, "topup-1", []byte{9})
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != "refill_refused" {
		t.Fatalf("expected refill_refused on a bounded offering, got %v", err)
	}
	// The session is untouched — a refused top-up is not a winddown.
	rec, _ := h.store.Get(res.SessionID)
	if rec.Terminal() {
		t.Fatal("refused top-up wound the session down")
	}
}

// ---------------------------------------------------------------------------
// payee identity and ticket rejection

// TestOpenDerivesWorkIDFromPayment pins the identity rule: the payee
// daemon binds its session — and the recipient rand every ticket was
// minted against — to the payment's recipient_rand_hash. A work_id of
// our own invention would bind the session to a rand the sender never
// saw, and nothing it paid with could validate against it.
func TestOpenDerivesWorkIDFromPayment(t *testing.T) {
	h := newHarness(t)
	hash := []byte("0123456789abcdef0123456789abcdef")
	raw, err := proto.Marshal(&pb.Payment{
		TicketParams: &pb.TicketParams{RecipientRandHash: hash},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.engine.Open(context.Background(), OpenRequest{
		RequestID: "req-1", PaymentBytes: raw, Spec: h.spec,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	want := hex.EncodeToString(hash)
	if res.WorkID != want {
		t.Fatalf("work id = %q; want the payment's recipient_rand_hash %q", res.WorkID, want)
	}
	// Every daemon call for this session must carry that same id, or
	// the lifecycle spans two payee sessions.
	h.pay.mu.Lock()
	defer h.pay.mu.Unlock()
	if len(h.pay.workIDs) == 0 {
		t.Fatal("daemon was never called")
	}
	for _, got := range h.pay.workIDs {
		if got != want {
			t.Fatalf("daemon call used work id %q; want %q", got, want)
		}
	}
}

// TestOpenFallsBackToRequestIDForStubPayment keeps in-process stubs and
// fixtures working: bytes that carry no ticket params have no payee
// session to collide with, so the request id stands in — the same
// fallback the job path takes.
func TestOpenFallsBackToRequestIDForStubPayment(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	if res.WorkID != "req-1" {
		t.Fatalf("work id = %q; want the request id", res.WorkID)
	}
}

// TestOpenFailsClosedWhenEveryTicketRejected pins the other half: the
// daemon reports an all-rejected batch in its result, not as an error.
// Opening anyway yields a session with no funded runway that dies at the
// first lease check, which reads as a broker fault rather than the
// payment fault it is.
func TestOpenFailsClosedWhenEveryTicketRejected(t *testing.T) {
	h := newHarness(t)
	h.pay.ticketCount, h.pay.ticketsBad = 2, 2
	h.pay.rejectReason = payment.PaymentRejectionReasonInvalidRecipientRand

	_, err := h.engine.Open(context.Background(), OpenRequest{
		RequestID: "req-1", PaymentBytes: []byte{1, 2, 3}, Spec: h.spec,
		CapacityRef: "cap-slot-1",
	})
	// recipient_rotated rather than a generic payment failure: the
	// gateway's remedy is mechanical, and it should act on a code.
	var perr *ProtocolError
	if !errors.As(err, &perr) || perr.Code != "recipient_rotated" {
		t.Fatalf("err = %v; want a recipient_rotated ProtocolError", err)
	}
	if !strings.Contains(perr.Detail, "rotated") {
		t.Fatalf("detail = %q; want the rotation named", perr.Detail)
	}
	if h.runner.created != 0 {
		t.Fatal("runner was bound against a payment that funded nothing")
	}
	if h.pay.closed == 0 {
		t.Fatal("payee session left open after a failed open")
	}
	if len(h.release) != 1 {
		t.Fatalf("capacity releases = %d; want the reserved slot given back", len(h.release))
	}
}

// TestOpenAcceptsPartiallyRejectedBatch: a partial rejection still
// credits something. The balance it produced is the honest one, and the
// session's own runway checks enforce the consequences.
func TestOpenAcceptsPartiallyRejectedBatch(t *testing.T) {
	h := newHarness(t)
	h.pay.ticketCount, h.pay.ticketsBad = 3, 1
	h.pay.rejectReason = payment.PaymentRejectionReasonNonceReplay

	if _, err := h.engine.Open(context.Background(), OpenRequest{
		RequestID: "req-1", PaymentBytes: []byte{1, 2, 3}, Spec: h.spec,
	}); err != nil {
		t.Fatalf("open: %v", err)
	}
}

// TestTopUpRefusesAllRejectedBatch: an all-rejected top-up must not
// extend the lease, or the session runs on runway nobody funded.
func TestTopUpRefusesAllRejectedBatch(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	before, err := h.store.Get(res.SessionID)
	if err != nil {
		t.Fatal(err)
	}

	h.pay.ticketCount, h.pay.ticketsBad = 2, 2
	h.pay.rejectReason = payment.PaymentRejectionReasonInvalidSignature
	if _, err := h.engine.TopUp(context.Background(), res.SessionID, "topup-1", []byte{9}); err == nil {
		t.Fatal("top-up accepted a batch the payee rejected in full")
	}

	after, err := h.store.Get(res.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.LeaseExpiresAt.Equal(before.LeaseExpiresAt) {
		t.Fatalf("lease moved from %s to %s on a refused top-up",
			before.LeaseExpiresAt, after.LeaseExpiresAt)
	}
}

// TestOpenCarriesPriceDenominatorToDaemon: the daemon multiplies price by
// units and divides by this. Omitting it bills per_units times the
// intended rate, which is invisible in every test that prices at 1.
func TestOpenCarriesPriceDenominatorToDaemon(t *testing.T) {
	h := newHarness(t)
	h.spec.PerUnits = 1000

	h.open(t)

	h.pay.mu.Lock()
	defer h.pay.mu.Unlock()
	if len(h.pay.openPerUnits) == 0 {
		t.Fatal("daemon session was never opened")
	}
	if got := h.pay.openPerUnits[0]; got != 1000 {
		t.Fatalf("daemon was told per_units = %d; want the offering's 1000", got)
	}
}

// ---------------------------------------------------------------------------
// top-up idempotency

// TestTopUpReplayReturnsRecordedOutcome: a retry after a lost response
// must not fund the session again. With identical bytes the daemon's
// nonce dedup would absorb it, but an SDK retry mints a fresh envelope —
// so the broker has to answer from its own record.
func TestTopUpReplayReturnsRecordedOutcome(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)

	first, err := h.engine.TopUp(context.Background(), res.SessionID, "topup-1", []byte{9})
	if err != nil {
		t.Fatalf("topup: %v", err)
	}
	processCalls := len(h.pay.workIDs)

	replay, err := h.engine.TopUp(context.Background(), res.SessionID, "topup-1", []byte{9})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !replay.Lease.Equal(first.Lease) || replay.Balance.Cmp(first.Balance) != 0 {
		t.Fatalf("replay = (%v, %s); want the recorded (%v, %s)",
			replay.Lease, replay.Balance, first.Lease, first.Balance)
	}
	if len(h.pay.workIDs) != processCalls {
		t.Fatal("replay reached the payment daemon; it must answer from the record")
	}
}

// TestTopUpRejectsReusedRequestIDWithDifferentEnvelope: the id is a
// promise about content. A different envelope under the same id is a
// caller bug, and answering it with the first top-up's outcome would
// silently swallow funding.
func TestTopUpRejectsReusedRequestIDWithDifferentEnvelope(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	if _, err := h.engine.TopUp(context.Background(), res.SessionID, "topup-1", []byte{9}); err != nil {
		t.Fatal(err)
	}
	_, err := h.engine.TopUp(context.Background(), res.SessionID, "topup-1", []byte{7, 7})
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != "request_id_reuse" {
		t.Fatalf("err = %v; want request_id_reuse", err)
	}
}

// TestTopUpRequiresRequestID: without the key there is no safe retry,
// which is the whole defect this closes.
func TestTopUpRequiresRequestID(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	_, err := h.engine.TopUp(context.Background(), res.SessionID, "", []byte{9})
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != "request_id_required" {
		t.Fatalf("err = %v; want request_id_required", err)
	}
}

// TestTopUpNonceReplayReadsAsAlreadyCredited covers the crash window
// between the daemon's credit and the broker's idempotency record: the
// retry re-presents nonces the daemon has seen, and every ticket bounces.
// That is not a payment failure — the money landed the first time — so
// the caller gets the current lease back, unextended.
func TestTopUpNonceReplayReadsAsAlreadyCredited(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	before, _ := h.store.Get(res.SessionID)

	h.pay.ticketCount, h.pay.ticketsBad = 2, 2
	h.pay.rejectReason = payment.PaymentRejectionReasonNonceReplay

	out, err := h.engine.TopUp(context.Background(), res.SessionID, "topup-1", []byte{9})
	if err != nil {
		t.Fatalf("nonce replay must not read as a payment failure: %v", err)
	}
	if !out.Lease.Equal(before.LeaseExpiresAt) {
		t.Fatalf("lease moved to %v; an already-credited envelope buys no new runway", out.Lease)
	}
	after, _ := h.store.Get(res.SessionID)
	if !after.LeaseExpiresAt.Equal(before.LeaseExpiresAt) {
		t.Fatal("persisted lease moved on an already-credited envelope")
	}
}

// ---------------------------------------------------------------------------
// recipient rotation

// rotatedPayment builds a payment whose derived identity differs from
// the session's — what a gateway mints after its payee rotated.
func rotatedPayment(t *testing.T, randHash string) []byte {
	t.Helper()
	raw, err := proto.Marshal(&pb.Payment{
		TicketParams: &pb.TicketParams{RecipientRandHash: []byte(randHash)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// TestRebindMovesSessionToRotatedIdentity: the gap this closes. A live
// session whose payee rotated has no other way forward — the broker
// binds one work_id for life and every later payment is rejected.
func TestRebindMovesSessionToRotatedIdentity(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	before, _ := h.store.Get(res.SessionID)

	successor := rotatedPayment(t, "successor-rand-000000000000000000")
	out, err := h.engine.TopUpRebind(context.Background(), res.SessionID,
		"rebind-1", before.WorkID, successor)
	if err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if !out.Lease.After(before.LeaseExpiresAt) && !out.Lease.Equal(before.LeaseExpiresAt) {
		t.Fatalf("rebind shortened the lease: %v -> %v", before.LeaseExpiresAt, out.Lease)
	}

	after, _ := h.store.Get(res.SessionID)
	wantWorkID := hex.EncodeToString([]byte("successor-rand-000000000000000000"))
	if after.WorkID != wantWorkID {
		t.Fatalf("work_id = %q; want the successor identity %q", after.WorkID, wantWorkID)
	}
	if after.PredecessorWorkID != before.WorkID {
		t.Fatalf("predecessor = %q; want %q", after.PredecessorWorkID, before.WorkID)
	}
	if after.RotationGeneration != 1 {
		t.Fatalf("generation = %d; want 1", after.RotationGeneration)
	}
	// Continuity is the whole point: the session, its credential and its
	// cumulative accounting do not move.
	if after.SessionID != before.SessionID || after.RunnerSessionID != before.RunnerSessionID {
		t.Fatal("rebind disturbed session or runner identity")
	}
	if !bytes.Equal(after.CredentialHash, before.CredentialHash) {
		t.Fatal("rebind re-issued the session credential")
	}
	if after.DebitedTotal != before.DebitedTotal || after.ClaimedTotal != before.ClaimedTotal {
		t.Fatal("cumulative accounting reset across the rotation")
	}
	if after.DebitSeq != 0 {
		t.Fatalf("debit_seq = %d; the successor's sequence space starts fresh", after.DebitSeq)
	}
}

// TestRebindRefusesWrongPredecessor: the declaration is checked against
// the session, so a gateway that retries the wrong session's top-up gets
// a refusal rather than a silent identity change.
func TestRebindRefusesWrongPredecessor(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)

	_, err := h.engine.TopUpRebind(context.Background(), res.SessionID,
		"rebind-1", "some-other-sessions-work-id", rotatedPayment(t, "successor-rand-000000000000000000"))
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != "rebind_refused" {
		t.Fatalf("err = %v; want rebind_refused", err)
	}
}

// TestRebindRefusesSuccessorThatDoesNotCredit is guard 2, the one that
// does the real work: tickets minted against a fake or stale identity
// cannot validate, so a batch the payee rejects in full proves nothing
// and the session stays where it is.
func TestRebindRefusesSuccessorThatDoesNotCredit(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	before, _ := h.store.Get(res.SessionID)

	h.pay.ticketCount, h.pay.ticketsBad = 2, 2
	h.pay.rejectReason = payment.PaymentRejectionReasonInvalidRecipientRand

	_, err := h.engine.TopUpRebind(context.Background(), res.SessionID,
		"rebind-1", before.WorkID, rotatedPayment(t, "successor-rand-000000000000000000"))
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != "rebind_refused" {
		t.Fatalf("err = %v; want rebind_refused", err)
	}
	after, _ := h.store.Get(res.SessionID)
	if after.WorkID != before.WorkID || after.RotationGeneration != 0 {
		t.Fatal("session moved onto an identity that never credited")
	}
}

// TestRebindSettlesPredecessorBeforeClosing: carrying unsettled work
// across an identity change would make the ledger unauditable, so the
// outstanding units are debited against the OLD work_id first.
func TestRebindSettlesPredecessorBeforeClosing(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	before, _ := h.store.Get(res.SessionID)

	// Claim without debiting, the shape a crash mid-commit leaves.
	if err := h.store.Update(res.SessionID, func(r *sessionstore.Record) error {
		r.ClaimedTotal = 40
		r.DebitedTotal = 25
		r.DebitSeq = 3
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := h.engine.TopUpRebind(context.Background(), res.SessionID,
		"rebind-1", before.WorkID, rotatedPayment(t, "successor-rand-000000000000000000")); err != nil {
		t.Fatalf("rebind: %v", err)
	}

	var settled bool
	h.pay.mu.Lock()
	for _, d := range h.pay.debits {
		if d.units == 15 && d.seq == 4 {
			settled = true
		}
	}
	h.pay.mu.Unlock()
	if !settled {
		t.Fatalf("predecessor was not settled for its 15 outstanding units: %+v", h.pay.debits)
	}
	after, _ := h.store.Get(res.SessionID)
	if after.DebitedTotal != 40 {
		t.Fatalf("debited total = %d; want the claim settled at 40", after.DebitedTotal)
	}
	if after.GenerationStartUnits != 40 {
		t.Fatalf("generation start = %d; want 40, so the new generation's subtotal starts at zero",
			after.GenerationStartUnits)
	}
}

// TestRebindStopsAtTheRotationBound: an unbounded rotate-and-rebind loop
// would burn the payer's deposit without ever delivering work, so the
// session ends instead — naming the consequence, not the mechanism.
func TestRebindStopsAtTheRotationBound(t *testing.T) {
	h := newHarness(t)
	h.spec.MaxRotations = 1
	res := h.open(t)
	before, _ := h.store.Get(res.SessionID)

	if _, err := h.engine.TopUpRebind(context.Background(), res.SessionID,
		"rebind-1", before.WorkID, rotatedPayment(t, "successor-rand-000000000000000000")); err != nil {
		t.Fatalf("first rebind: %v", err)
	}
	mid, _ := h.store.Get(res.SessionID)

	_, err := h.engine.TopUpRebind(context.Background(), res.SessionID,
		"rebind-2", mid.WorkID, rotatedPayment(t, "third-rand-00000000000000000000"))
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != "rebind_refused" {
		t.Fatalf("err = %v; want rebind_refused at the bound", err)
	}
	final, _ := h.store.Get(res.SessionID)
	if !final.Terminal() {
		t.Fatal("session survived its rotation bound with no way to fund itself")
	}
	if final.CloseReason != ReasonPaymentUnrecoverable {
		t.Fatalf("close reason = %q; want %q — the consequence, not the mechanism",
			final.CloseReason, ReasonPaymentUnrecoverable)
	}
}

// TestRebindRefusesWhenTheLastGenerationDeliveredNothing: the bound that
// catches a loop early. A generation that debited no units bought
// nothing, so funding another rebind is throwing good money after bad.
func TestRebindRefusesWhenTheLastGenerationDeliveredNothing(t *testing.T) {
	h := newHarness(t)
	h.spec.MaxRotations = 5
	res := h.open(t)
	before, _ := h.store.Get(res.SessionID)

	if _, err := h.engine.TopUpRebind(context.Background(), res.SessionID,
		"rebind-1", before.WorkID, rotatedPayment(t, "successor-rand-000000000000000000")); err != nil {
		t.Fatalf("first rebind: %v", err)
	}
	mid, _ := h.store.Get(res.SessionID)
	// No usage debited on the new generation, so the rebind bought
	// nothing.
	_, err := h.engine.TopUpRebind(context.Background(), res.SessionID,
		"rebind-2", mid.WorkID, rotatedPayment(t, "third-rand-00000000000000000000"))
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != "rebind_refused" {
		t.Fatalf("err = %v; want rebind_refused on a rotation that delivered nothing", err)
	}
	final, _ := h.store.Get(res.SessionID)
	if final.CloseReason != ReasonPaymentUnrecoverable {
		t.Fatalf("close reason = %q; want %q", final.CloseReason, ReasonPaymentUnrecoverable)
	}
}

// ---------------------------------------------------------------------------
// settlement

// TestSettlementReportsCumulativeAccounting: LOC bills off this record,
// and the authoritative quantity is what the ledger moved, not what a
// runner claimed. billed_value must be one ceiling over the cumulative
// total so a reader recomputing it agrees.
func TestSettlementReportsCumulativeAccounting(t *testing.T) {
	h := newHarness(t)
	h.spec.PricePerWorkUnitWei = big.NewInt(100)
	h.spec.PerUnits = 1000
	res := h.open(t)

	for i, total := range []uint64{7, 19, 31} {
		if _, err := h.engine.ProcessEvent(context.Background(), res.SessionID,
			usageEvent(fmt.Sprintf("ev-%d", i), uint64(i+1), total)); err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
	}

	rec, _ := h.store.Get(res.SessionID)
	set := h.engine.SettlementFor(rec, h.spec)
	if set == nil {
		t.Fatal("no settlement record")
	}
	if set.GetDebitedUnits() != 31 || set.GetClaimedUnits() != 31 {
		t.Fatalf("units: claimed=%d debited=%d; want 31/31",
			set.GetClaimedUnits(), set.GetDebitedUnits())
	}
	// ceil(31 * 100 / 1000) = 4, not 3 (floor) and not the 6 that
	// pricing each event separately would give (1+2+2).
	if got := new(big.Int).SetBytes(set.GetBilledValueWei().GetValue()); got.Int64() != 4 {
		t.Fatalf("billed = %s wei; want 4 — one ceiling over the cumulative total", got)
	}
	if set.GetSessionId() != res.SessionID || set.GetWorkId() != rec.WorkID {
		t.Fatal("settlement does not identify its session")
	}
	if set.GetPerUnits() != 1000 {
		t.Fatalf("per_units = %d; a reader cannot recompute without it", set.GetPerUnits())
	}
}

// TestSettlementCarriesTheRotationChain: after a rebind the record has to
// explain which identity paid for which stretch, because that is all LOC
// gets — a completed rotation is settlement-only.
func TestSettlementCarriesTheRotationChain(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	before, _ := h.store.Get(res.SessionID)

	if _, err := h.engine.ProcessEvent(context.Background(), res.SessionID,
		usageEvent("ev-1", 1, 10)); err != nil {
		t.Fatal(err)
	}
	if _, err := h.engine.TopUpRebind(context.Background(), res.SessionID,
		"rebind-1", before.WorkID, rotatedPayment(t, "successor-rand-000000000000000000")); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	if _, err := h.engine.ProcessEvent(context.Background(), res.SessionID,
		usageEvent("ev-2", 2, 25)); err != nil {
		t.Fatal(err)
	}

	rec, _ := h.store.Get(res.SessionID)
	set := h.engine.SettlementFor(rec, h.spec)
	if set.GetRotationGeneration() != 1 {
		t.Fatalf("generation = %d; want 1", set.GetRotationGeneration())
	}
	if set.GetPredecessorWorkId() != before.WorkID {
		t.Fatalf("predecessor = %q; want %q", set.GetPredecessorWorkId(), before.WorkID)
	}
	if set.GetDebitedUnits() != 25 {
		t.Fatalf("cumulative debited = %d; want 25 spanning both generations", set.GetDebitedUnits())
	}
	// The second generation delivered 15 of those 25.
	if set.GetGenerationDebitedUnits() != 15 {
		t.Fatalf("generation subtotal = %d; want 15", set.GetGenerationDebitedUnits())
	}
}

// TestSettlementSeqIsPerSession: rotation mints a new work_id, so a
// per-identity counter would restart mid-session and leave a reader
// unable to order two records from one session.
func TestSettlementSeqIsPerSession(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	before, _ := h.store.Get(res.SessionID)

	first, err := h.engine.RecordSettlement(context.Background(), res.SessionID)
	if err != nil || first == nil {
		t.Fatalf("record: %v", err)
	}
	if _, err := h.engine.TopUpRebind(context.Background(), res.SessionID,
		"rebind-1", before.WorkID, rotatedPayment(t, "successor-rand-000000000000000000")); err != nil {
		t.Fatal(err)
	}
	second, err := h.engine.RecordSettlement(context.Background(), res.SessionID)
	if err != nil || second == nil {
		t.Fatalf("record: %v", err)
	}
	if second.GetSettlementSeq() <= first.GetSettlementSeq() {
		t.Fatalf("seq did not advance across a rotation: %d -> %d",
			first.GetSettlementSeq(), second.GetSettlementSeq())
	}
	if second.GetWorkId() == first.GetWorkId() {
		t.Fatal("test did not actually rotate")
	}
}
