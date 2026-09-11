package sessionengine

import (
	"bytes"
	"context"
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
	failClose    int   // fail the next N CloseSession calls
	openDelay    time.Duration
	ticketsBad   int32 // how many of them were rejected
	rejectReason payment.PaymentRejectionReason
	// pricing reads the offering under test at call time, so a test that
	// changes the price after the harness is built still gets a fake
	// that bills the way the real ledger would.
	pricing         func() (*big.Int, uint64)
	debitedUnits    uint64
	accountAdvances []payment.AdvanceAuthorizationRequest
	accountSettles  []payment.SettleAuthorizationRequest
}

func (f *fakePayment) FundWholesaleAccount(context.Context, []byte) (*payment.FundWholesaleAccountResult, error) {
	return &payment.FundWholesaleAccountResult{Account: &payment.WholesaleAccount{Available: big.NewInt(1000)}, Credited: big.NewInt(100)}, nil
}

func (f *fakePayment) AdmitAuthorization(_ context.Context, req payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	f.mu.Lock()
	delay := f.openDelay
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
	var auth pb.SpendAuthorization
	if err := proto.Unmarshal(req.AuthorizationBytes, &auth); err != nil || auth.GetPayload() == nil {
		return nil, errors.New("bad authorization")
	}
	payer := append([]byte(nil), auth.GetPayload().GetPayer()...)
	reserved := new(big.Int)
	if req.Reservation != nil {
		reserved.Set(req.Reservation)
	}
	f.mu.Lock()
	f.openCalls++
	f.mu.Unlock()
	return &payment.AdmitAuthorizationResult{State: int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED), Account: &payment.WholesaleAccount{Payer: payer, Payee: auth.GetPayload().GetPayee(), Available: big.NewInt(1000)}, Reserved: reserved, Credited: new(big.Int)}, nil
}
func (f *fakePayment) AdvanceAuthorization(_ context.Context, req payment.AdvanceAuthorizationRequest) (*payment.AdvanceAuthorizationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failDebits > 0 {
		f.failDebits--
		return nil, errors.New("transient daemon failure")
	}
	f.accountAdvances = append(f.accountAdvances, req)
	price, per := f.pricing()
	before := payment.BillFor(price, per, f.debitedUnits)
	after := payment.BillFor(price, per, req.CumulativeUnits)
	charged := new(big.Int).Sub(after, before)
	if req.CumulativeUnits > f.debitedUnits {
		f.debits = append(f.debits, debitCall{units: int64(req.CumulativeUnits - f.debitedUnits), seq: req.AdvanceSeq})
		f.debitedUnits = req.CumulativeUnits
	}
	return &payment.AdvanceAuthorizationResult{State: int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED), Account: &payment.WholesaleAccount{Payer: req.Payer, Available: big.NewInt(900)}, BilledDelta: charged, CumulativeBilled: after, Reserved: new(big.Int).Set(req.TargetReserved)}, nil
}
func (f *fakePayment) SettleAuthorization(_ context.Context, req payment.SettleAuthorizationRequest) (*payment.SettleAuthorizationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accountSettles = append(f.accountSettles, req)
	f.closed++
	price, per := f.pricing()
	billed := payment.BillFor(price, per, req.ActualUnits)
	return &payment.SettleAuthorizationResult{State: int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED), Account: &payment.WholesaleAccount{Payer: req.Payer, Available: big.NewInt(970)}, Billed: billed, Released: big.NewInt(20)}, nil
}
func (f *fakePayment) GetWholesaleAccount(context.Context, []byte) (*payment.WholesaleAccount, error) {
	return &payment.WholesaleAccount{Available: big.NewInt(1000)}, nil
}
func (f *fakePayment) GetSpendAuthorization(context.Context, []byte, string) (*payment.SpendAuthorizationStatus, error) {
	return &payment.SpendAuthorizationStatus{State: int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED), Reserved: big.NewInt(50)}, nil
}

func newFakePayment() *fakePayment {
	return &fakePayment{debitSeqSeen: map[uint64]int64{}, sufficient: true, balance: big.NewInt(1000)}
}

func (f *fakePayment) GetTicketParams(context.Context, payment.GetTicketParamsRequest) (*payment.TicketParams, error) {
	return nil, errors.New("unused")
}
func (f *fakePayment) OpenSession(_ context.Context, req payment.OpenSessionRequest) (*payment.OpenSessionResult, error) {
	f.mu.Lock()
	delay := f.openDelay
	f.mu.Unlock()
	if delay > 0 {
		time.Sleep(delay)
	}
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
func (f *fakePayment) DebitBalance(_ context.Context, req payment.DebitBalanceRequest) (*payment.DebitResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sessionGone {
		return nil, errors.New("session not found")
	}
	if f.failDebits > 0 {
		f.failDebits--
		return nil, errors.New("transient daemon failure")
	}
	// Idempotent by seq, like the real daemon. The charge is reported
	// cumulatively, also like the real daemon: a debit costs
	// bill(after) - bill(before), so the fake must not hand back an
	// independent per-debit ceiling or tests would encode the very
	// arithmetic the ledger disagrees with.
	replayed := true
	if _, seen := f.debitSeqSeen[req.DebitSeq]; !seen {
		f.debitSeqSeen[req.DebitSeq] = req.WorkUnits
		f.debits = append(f.debits, debitCall{req.WorkUnits, req.DebitSeq})
		replayed = false
	}
	charged := new(big.Int)
	if !replayed {
		price, perUnits := f.price()
		before := payment.BillFor(price, perUnits, f.debitedUnits)
		f.debitedUnits += uint64(req.WorkUnits)
		charged = new(big.Int).Sub(payment.BillFor(price, perUnits, f.debitedUnits), before)
	}
	return &payment.DebitResult{
		Balance:         big.NewInt(0),
		DebitedWei:      charged,
		CumulativeUnits: f.debitedUnits,
		Replayed:        replayed,
	}, nil
}

func (f *fakePayment) price() (*big.Int, uint64) {
	if f.pricing != nil {
		if p, u := f.pricing(); p != nil {
			return p, u
		}
	}
	return big.NewInt(1), 1
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
	if f.failClose > 0 {
		f.failClose--
		return errors.New("payment daemon unavailable")
	}
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
	mu            sync.Mutex
	created       int
	terminated    []string
	gone          bool
	runtime       json.RawMessage
	failCreate    bool
	failTerminate bool
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
	if f.failTerminate {
		return errors.New("runner unreachable")
	}
	f.terminated = append(f.terminated, reason)
	return nil
}

// ---------------------------------------------------------------------------
// harness

type harness struct {
	engine    *Engine
	store     *sessionstore.Store
	pay       *fakePayment
	runner    *fakeRunner
	spec      *OfferingSpec
	nowVal    time.Time
	nowMu     sync.Mutex
	release   []string
	winddowns []string // OnWinddown reasons, in order
}

func TestNewRefusesUndrainedLegacyNonterminalSession(t *testing.T) {
	key := make([]byte, sessionstore.KeySize)
	st, err := sessionstore.Open(filepath.Join(t.TempDir(), "legacy.db"), key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.CreateIndexed(&sessionstore.Record{SessionID: "legacy-active", GatewaySessionID: "gateway-legacy", State: sessionstore.StateActive}, "legacy-request"); err != nil {
		t.Fatal(err)
	}
	_, err = New(Config{
		Store: st, Payment: newFakePayment(), Runner: func(string) RunnerClient { return &fakeRunner{} },
		Specs: func(string) *OfferingSpec { return &OfferingSpec{} },
	})
	if err == nil || !strings.Contains(err.Error(), "authorization-only cutover refused") {
		t.Fatalf("New error = %v; want explicit cutover refusal", err)
	}
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
		Capability:          "meet:sfu-room",
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
		OnWinddown:      func(_ sessionstore.Record, reason string) { h.winddowns = append(h.winddowns, reason) },
		Now:             h.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	h.pay.pricing = func() (*big.Int, uint64) {
		if h.spec == nil {
			return nil, 0
		}
		return h.spec.PricePerWorkUnitWei, h.spec.PerUnits
	}
	h.engine = eng
	return h
}

func (h *harness) authorization(t *testing.T, authorizationID, requestID, sessionID string, maxUnits uint64) []byte {
	t.Helper()
	payload := &pb.SpendAuthorizationPayload{
		Domain: "livepeer-spend-authorization/v1", AuthorizationId: authorizationID, RequestId: requestID, SessionId: sessionID,
		Payer: bytes.Repeat([]byte{0x11}, 20), Payee: bytes.Repeat([]byte{0x22}, 20), Protocol: "paid-session/v1", Capability: h.spec.Capability, Offering: h.spec.Offering,
		AcceptedPrice: &pb.AcceptedPrice{PricePerUnitWei: &pb.BigUInt{Value: big.NewInt(10).Bytes()}, UnitsPerPrice: 1, WorkUnitName: h.spec.WorkUnit},
		MaxDebitWei:   &pb.BigUInt{Value: big.NewInt(1000).Bytes()}, MaxTotalUnits: maxUnits,
	}
	wire, err := proto.Marshal(&pb.SpendAuthorization{Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

func (h *harness) open(t *testing.T) *OpenResult {
	t.Helper()
	res, err := h.engine.Open(context.Background(), OpenRequest{
		RequestID:             "req-1",
		GatewaySessionID:      "gws-1",
		SessionParams:         json.RawMessage(`{"room_hint":"standup"}`),
		PaymentBytes:          []byte{1, 2, 3},
		AuthorizationBytes:    h.authorization(t, "auth-req-1", "req-1", "gws-1", 100),
		InitialReservationWei: big.NewInt(50),
		Spec:                  h.spec,
		CapacityRef:           "cap-slot-1",
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

func TestAccountBackedSessionUsesBoundedRunwayAndSettles(t *testing.T) {
	h := newHarness(t)
	payer, payee := bytes.Repeat([]byte{0x11}, 20), bytes.Repeat([]byte{0x22}, 20)
	payload := &pb.SpendAuthorizationPayload{
		AuthorizationId: "auth-session-1", RequestId: "req-account-1", SessionId: "gws-account-1",
		Payer: payer, Payee: payee, Protocol: "paid-session/v1", Capability: h.spec.Capability, Offering: h.spec.Offering,
		MaxDebitWei: &pb.BigUInt{Value: big.NewInt(1000).Bytes()}, MaxTotalUnits: 100,
	}
	wire, err := proto.Marshal(&pb.SpendAuthorization{Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := h.engine.Open(context.Background(), OpenRequest{
		RequestID: "req-account-1", GatewaySessionID: "gws-account-1",
		SessionParams: json.RawMessage(`{"room_hint":"account"}`), AuthorizationBytes: wire,
		InitialReservationWei: big.NewInt(50), Spec: h.spec, CapacityRef: "cap-account",
	})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := h.store.Get(opened.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if rec.AccountAuthorizationID != "auth-session-1" || rec.AuthorizationReservedWei != "50" || h.pay.openCalls != 1 {
		t.Fatalf("account session binding=%+v authorization_admissions=%d", rec, h.pay.openCalls)
	}
	if _, err := h.engine.ProcessEvent(context.Background(), opened.SessionID, usageEvent("account-tick-1", 1, 3)); err != nil {
		t.Fatal(err)
	}
	if len(h.pay.accountAdvances) != 1 || h.pay.accountAdvances[0].CumulativeUnits != 3 || h.pay.accountAdvances[0].TargetReserved.Cmp(big.NewInt(50)) != 0 {
		t.Fatalf("account advances=%+v", h.pay.accountAdvances)
	}
	over := uint64(120)
	outcome, err := h.engine.ProcessEvent(context.Background(), opened.SessionID, Event{EventID: "account-tick-cap", Sequence: 2, EventType: "session.usage.tick", UsageUnit: "participant_minutes", UsageTot: &over})
	if err != nil || !outcome.Terminal {
		t.Fatalf("cap-crossing event outcome=%+v err=%v", outcome, err)
	}
	if len(h.pay.accountAdvances) != 2 || h.pay.accountAdvances[1].CumulativeUnits != 100 || h.pay.accountAdvances[1].TargetReserved.Sign() != 0 {
		t.Fatalf("cap-crossing advance=%+v", h.pay.accountAdvances)
	}
	if len(h.pay.accountSettles) != 1 || h.pay.accountSettles[0].AuthorizationID != "auth-session-1" || h.pay.accountSettles[0].ActualUnits != 100 {
		t.Fatalf("account settlements=%+v", h.pay.accountSettles)
	}
	rec, err = h.store.Get(opened.SessionID)
	if err != nil || rec.CloseReason != ReasonAuthorizationExhausted {
		t.Fatalf("closed session=%+v err=%v", rec, err)
	}
}

// TestOpenIdempotentReplay: an identical retry converges on the USABLE
// recorded outcome. Withholding the credential would leave a gateway
// whose response was lost holding a funded session it can never drive —
// which defeats the point of making opens idempotent.
func TestOpenIdempotentReplay(t *testing.T) {
	h := newHarness(t)
	first := h.open(t)
	replay := h.open(t) // same request id, same content
	if !replay.Replayed || replay.SessionID != first.SessionID || replay.WorkID != first.WorkID {
		t.Fatalf("replay must return the original session: %+v", replay)
	}
	if replay.Credential != first.Credential {
		t.Fatalf("replay credential = %q; want the recorded %q", replay.Credential, first.Credential)
	}
	if len(replay.Grants) != len(first.Grants) {
		t.Fatalf("replay returned %d grants; want the recorded %d", len(replay.Grants), len(first.Grants))
	}
	if h.runner.created != 1 {
		t.Fatalf("replay created a second runner session: %d", h.runner.created)
	}
}

// TestOpenRefusesReusedRequestIDWithDifferentContent: the id is a
// promise about content, and re-delivering a credential makes breaking
// that promise worse than before — the caller would receive the keys to
// a session it did not open.
func TestOpenRefusesReusedRequestIDWithDifferentContent(t *testing.T) {
	h := newHarness(t)
	h.open(t)
	_, err := h.engine.Open(context.Background(), OpenRequest{
		RequestID: "req-1", GatewaySessionID: "gws-1",
		SessionParams: json.RawMessage(`{"room_hint":"something-else"}`), PaymentBytes: []byte{9, 9, 9},
		AuthorizationBytes: h.authorization(t, "auth-req-1", "req-1", "gws-1", 100), InitialReservationWei: big.NewInt(50), Spec: h.spec,
	})
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != "request_id_reuse" {
		t.Fatalf("err = %v; want request_id_reuse", err)
	}
}

// TestReplayMaterialIsClearedAtWinddown: the replay window is the
// session's life. Secrets outliving what they unlock is how a store
// becomes a liability.
func TestReplayMaterialIsClearedAtWinddown(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	if _, err := h.engine.End(context.Background(), res.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	rec, err := h.store.Get(res.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.ReplayMaterial) != 0 || len(rec.ReplayMaterialSealed) != 0 {
		t.Fatal("credential and grants survived the session that used them")
	}
}

func TestOpenFailsClosedOnBadDescriptor(t *testing.T) {
	h := newHarness(t)
	h.runner.runtime = json.RawMessage(`{"schema":"sfu-room/v1","public":{},"surprise":{}}`)
	_, err := h.engine.Open(context.Background(), OpenRequest{
		RequestID: "req-x", GatewaySessionID: "gws-x", PaymentBytes: []byte{1}, Spec: h.spec, CapacityRef: "slot",
		AuthorizationBytes: h.authorization(t, "auth-x", "req-x", "gws-x", 100), InitialReservationWei: big.NewInt(50),
	})
	if err == nil {
		t.Fatal("expected descriptor rejection")
	}
	if len(h.runner.terminated) != 1 {
		t.Fatal("runner session not terminated on fail-closed open")
	}
	if len(h.pay.accountSettles) != 1 {
		t.Fatal("authorization reservation not released on fail-closed open")
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

func TestOutputHealthPersistsAndContinuousStallFailsClosed(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	_ = h.store.Update(res.SessionID, func(r *sessionstore.Record) error {
		r.LeaseExpiresAt = h.now().Add(time.Hour)
		return nil
	})
	since := h.now().Format(time.RFC3339Nano)
	stalled := func(id string, seq uint64) Event {
		return Event{EventID: id, Sequence: seq, EventType: "session.output.stalled",
			Details: json.RawMessage(fmt.Sprintf(`{"output_state":"stalled","output_state_since":%q,"last_failure_code":"encoder_init_failed"}`, since))}
	}
	if _, err := h.engine.ProcessEvent(context.Background(), res.SessionID, stalled("evt_stall_1", 1)); err != nil {
		t.Fatal(err)
	}
	first, _ := h.store.Get(res.SessionID)
	if first.OutputState != "stalled" || first.LastFailureCode != "encoder_init_failed" || first.OutputStalledAt.IsZero() {
		t.Fatalf("output health not persisted: %+v", first)
	}

	// A later stalled callback proves runner liveness but cannot reset the
	// broker-observed stall anchor.
	h.advance(59 * time.Second)
	if _, err := h.engine.ProcessEvent(context.Background(), res.SessionID, stalled("evt_stall_2", 2)); err != nil {
		t.Fatal(err)
	}
	second, _ := h.store.Get(res.SessionID)
	if !second.OutputStalledAt.Equal(first.OutputStalledAt) {
		t.Fatalf("repeated stall reset deadline: %s -> %s", first.OutputStalledAt, second.OutputStalledAt)
	}
	h.advance(2 * time.Second)
	h.engine.Sweep(context.Background())
	final, _ := h.store.Get(res.SessionID)
	if !final.Terminal() || final.CloseReason != ReasonOutputFailed {
		t.Fatalf("persistent stall did not fail closed: %+v", final)
	}
}

func TestFailedEventPreservesSafeReasonAndRejectsMalformedHealthAtomically(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	if _, err := h.engine.ProcessEvent(context.Background(), res.SessionID, Event{
		EventID: "evt_bad", Sequence: 1, EventType: "session.heartbeat",
		Details: json.RawMessage(`{"output_state":"broken"}`),
	}); err == nil {
		t.Fatal("malformed output health accepted")
	}
	rec, _ := h.store.Get(res.SessionID)
	if rec.LastSequence != 0 || rec.OutputState != "" {
		t.Fatalf("rejected health advanced record: %+v", rec)
	}
	total := uint64(0)
	out, err := h.engine.ProcessEvent(context.Background(), res.SessionID, Event{
		EventID: "evt_failed", Sequence: 1, EventType: "session.failed", Reason: "output_failed",
		UsageUnit: "participant_minutes", UsageTot: &total,
		Details: json.RawMessage(fmt.Sprintf(`{"output_state":"stalled","output_state_since":%q,"last_failure_code":"unknown"}`, h.now().Format(time.RFC3339Nano))),
	})
	if err != nil || !out.Terminal {
		t.Fatalf("failed event: %+v %v", out, err)
	}
	rec, _ = h.store.Get(res.SessionID)
	if rec.State != sessionstore.StateFailed || rec.CloseReason != ReasonOutputFailed || rec.LastFailureCode != "unknown" {
		t.Fatalf("terminal output health lost: %+v", rec)
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
func TestSettlementStateUsesTheNormativeVocabulary(t *testing.T) {
	// Every state the proto allows, and nothing else.
	normative := map[string]bool{"open": true, "winding_down": true, "closed": true}

	t.Run("interim record is open", func(t *testing.T) {
		h := newHarness(t)
		res := h.open(t)
		if _, err := h.engine.ProcessEvent(context.Background(), res.SessionID,
			usageEvent("ev-1", 1, 5)); err != nil {
			t.Fatal(err)
		}
		rec, _ := h.store.Get(res.SessionID)
		if rec.State != sessionstore.StateActive {
			t.Fatalf("precondition: internal state is %q, wanted %q",
				rec.State, sessionstore.StateActive)
		}
		set := h.engine.SettlementFor(rec, h.spec)
		if set == nil {
			t.Fatal("no settlement record")
		}
		if !normative[set.GetState()] {
			t.Fatalf("state = %q; the proto allows only open|winding_down|closed",
				set.GetState())
		}
		if set.GetState() != "open" {
			t.Fatalf("an active session settles as %q; want \"open\"", set.GetState())
		}
	})

	t.Run("terminal record is closed", func(t *testing.T) {
		h := newHarness(t)
		res := h.open(t)
		if _, err := h.engine.ProcessEvent(context.Background(), res.SessionID,
			usageEvent("ev-1", 1, 5)); err != nil {
			t.Fatal(err)
		}
		if _, err := h.engine.End(context.Background(), res.SessionID, "gateway_close"); err != nil {
			t.Fatalf("End: %v", err)
		}
		rec, _ := h.store.Get(res.SessionID)
		// The internal state really is "ended" — that is the value that
		// was leaking, so pin it, or the test could pass because the
		// internal vocabulary changed rather than because it is mapped.
		if rec.State != sessionstore.StateEnded {
			t.Fatalf("precondition: internal state is %q, wanted %q",
				rec.State, sessionstore.StateEnded)
		}
		set := h.engine.SettlementFor(rec, h.spec)
		if set == nil {
			t.Fatal("no settlement record")
		}
		if set.GetState() == sessionstore.StateEnded {
			t.Fatal(`state = "ended": the internal vocabulary reached the wire; ` +
				`a clearinghouse refuses this as session_not_terminal`)
		}
		if !normative[set.GetState()] {
			t.Fatalf("state = %q; the proto allows only open|winding_down|closed",
				set.GetState())
		}
		if set.GetState() != "closed" {
			t.Fatalf("an ended session settles as %q; want \"closed\"", set.GetState())
		}
	})
}

// A clearinghouse reconciles through the signed settlement lookup and does
// not hold the gateway's session credential. Preserve the terminal output
// diagnosis there so it need not trust an SDK callback to distinguish a
// healthy close from fail-closed zero output.
func TestSettlementCarriesTerminalOutputHealth(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	since := h.now().Add(-45 * time.Second).UTC()
	if err := h.store.Update(res.SessionID, func(rec *sessionstore.Record) error {
		rec.State = sessionstore.StateFailed
		rec.CloseReason = ReasonOutputFailed
		rec.OutputState = "stalled"
		rec.OutputStateSince = since
		rec.LastFailureCode = "encoder_init_failed"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec, _ := h.store.Get(res.SessionID)
	set := h.engine.SettlementFor(rec, h.spec)
	if set.GetBreakdown()["termination_reason"] != ReasonOutputFailed {
		t.Fatalf("termination_reason = %q", set.GetBreakdown()["termination_reason"])
	}
	if set.GetBreakdown()["output_state"] != "stalled" {
		t.Fatalf("output_state = %q", set.GetBreakdown()["output_state"])
	}
	if set.GetBreakdown()["output_state_since"] != since.Format(time.RFC3339Nano) {
		t.Fatalf("output_state_since = %q", set.GetBreakdown()["output_state_since"])
	}
	if set.GetBreakdown()["last_failure_code"] != "encoder_init_failed" {
		t.Fatalf("last_failure_code = %q", set.GetBreakdown()["last_failure_code"])
	}
}

// Two opens with one request id, concurrently: the id is claimed before
// any side effect, so exactly one payee session opens and one runner
// session is created; the other open is told it is in flight (or, if
// it arrived after the first finished, replays it).
func TestConcurrentOpensWithOneRequestIDFundOnce(t *testing.T) {
	h := newHarness(t)
	h.pay.openDelay = 150 * time.Millisecond
	open := func() (*OpenResult, error) {
		return h.engine.Open(context.Background(), OpenRequest{
			RequestID: "req-race", GatewaySessionID: "gws-race",
			SessionParams: json.RawMessage(`{}`), PaymentBytes: []byte{1, 2, 3},
			AuthorizationBytes: h.authorization(t, "auth-race", "req-race", "gws-race", 100), InitialReservationWei: big.NewInt(50),
			Spec: h.spec, CapacityRef: "slot-race",
		})
	}
	type out struct {
		res *OpenResult
		err error
	}
	results := make(chan out, 2)
	for i := 0; i < 2; i++ {
		go func() {
			r, err := open()
			results <- out{r, err}
		}()
	}
	var ok, inFlight, replayed int
	for i := 0; i < 2; i++ {
		o := <-results
		var pe *ProtocolError
		switch {
		case o.err == nil && o.res.Replayed:
			replayed++
		case o.err == nil:
			ok++
		case errors.As(o.err, &pe) && pe.Code == "open_in_flight":
			inFlight++
		default:
			t.Fatalf("unexpected: %+v %v", o.res, o.err)
		}
	}
	if ok != 1 || inFlight+replayed != 1 {
		t.Fatalf("ok=%d in_flight=%d replayed=%d; want one open and one refusal-or-replay", ok, inFlight, replayed)
	}
	if h.pay.openCalls != 1 || h.runner.created != 1 {
		t.Fatalf("payment opens=%d runner creates=%d; the guarantee is one of each", h.pay.openCalls, h.runner.created)
	}
	// And the reservation is gone: the record holds the id now.
	n := 0
	_ = h.store.ForEachReservation(func(sessionstore.OpenReservation) error { n++; return nil })
	if n != 0 {
		t.Fatalf("%d reservations left after a completed open", n)
	}
	// A third open with the same content replays; different content is refused.
	if r, err := open(); err != nil || !r.Replayed {
		t.Fatalf("replay: %+v %v", r, err)
	}
}

// A crash between payment and the session record leaves a reservation
// naming what was opened. Recover closes it, releases the capacity, and
// drops the reservation, so nothing funds a session nobody holds.
func TestRecoverUndoesAnAbandonedOpen(t *testing.T) {
	h := newHarness(t)
	if err := h.store.ReserveOpen("req-crash", []byte("fp")); err != nil {
		t.Fatal(err)
	}
	_ = h.store.UpdateReservation("req-crash", func(r *sessionstore.OpenReservation) error {
		r.Stage, r.WorkID, r.Sender, r.CapacityRef, r.BackendRef = sessionstore.ReservationRunnerCreated, "w-crash", []byte{9}, "slot-crash", "b1"
		r.RunnerSessionID = "rs-crash"
		r.AccountAuthorization = true
		return nil
	})
	h.engine.Recover(context.Background())
	if len(h.pay.accountSettles) != 1 {
		t.Fatalf("authorization settled %d times, want 1", len(h.pay.accountSettles))
	}
	if len(h.runner.terminated) != 1 || h.runner.terminated[0] != ReasonOpenFailed {
		t.Fatalf("runner terminated: %v", h.runner.terminated)
	}
	if len(h.release) != 1 || h.release[0] != "slot-crash" {
		t.Fatalf("capacity released: %v", h.release)
	}
	n := 0
	_ = h.store.ForEachReservation(func(sessionstore.OpenReservation) error { n++; return nil })
	if n != 0 {
		t.Fatal("reservation survived recovery")
	}
	// The id is free again: a retry of the open proceeds fresh.
	if _, err := h.engine.Open(context.Background(), OpenRequest{RequestID: "req-crash", GatewaySessionID: "g2",
		SessionParams: json.RawMessage(`{}`), PaymentBytes: []byte{1}, AuthorizationBytes: h.authorization(t, "auth-crash-2", "req-crash", "g2", 100), InitialReservationWei: big.NewInt(50), Spec: h.spec, CapacityRef: "slot-2"}); err != nil {
		t.Fatalf("retry after recovery: %v", err)
	}
}

// A winddown whose runner terminate fails stays winding_down — capacity
// held, no outcome, events refused — and Sweep completes it once the
// runner answers, firing the outcome exactly once.
func TestWinddownRetriesUntilObligationsAreMet(t *testing.T) {
	h := newHarness(t)
	res := h.open(t)
	h.runner.failTerminate = true
	if _, err := h.engine.End(context.Background(), res.SessionID, ""); err != nil {
		t.Fatal(err)
	}
	rec, _ := h.store.Get(res.SessionID)
	if rec.State != sessionstore.StateWindingDown || rec.Terminal() || rec.CloseReason != ReasonGatewayClose {
		t.Fatalf("after a failed terminate: %+v", rec)
	}
	if rec.PaymentClosed != true || rec.RunnerTerminated != false {
		t.Fatalf("obligation flags: payment=%v runner=%v", rec.PaymentClosed, rec.RunnerTerminated)
	}
	if len(h.release) != 0 || len(h.winddowns) != 0 {
		t.Fatalf("released %v / outcomes %v before the obligations were met", h.release, h.winddowns)
	}
	total := uint64(1)
	if _, err := h.engine.ProcessEvent(context.Background(), res.SessionID, usageEvent("evt_late", 1, total)); err == nil {
		t.Fatal("a winding-down session must refuse events")
	}
	// Still failing: the sweep retries and the state holds.
	h.engine.Sweep(context.Background())
	rec, _ = h.store.Get(res.SessionID)
	if rec.State != sessionstore.StateWindingDown || len(h.winddowns) != 0 {
		t.Fatalf("after a retry that also failed: %+v %v", rec, h.winddowns)
	}
	// The runner comes back: the next sweep completes the winddown.
	h.runner.failTerminate = false
	h.engine.Sweep(context.Background())
	rec, _ = h.store.Get(res.SessionID)
	if !rec.Terminal() || rec.State != sessionstore.StateEnded || !rec.RunnerTerminated || rec.EndedAt.IsZero() {
		t.Fatalf("after the runner answered: %+v", rec)
	}
	if len(h.release) != 1 || len(h.winddowns) != 1 || h.winddowns[0] != ReasonGatewayClose {
		t.Fatalf("released %v / outcomes %v; want one each, once", h.release, h.winddowns)
	}
	if h.pay.closed != 1 {
		t.Fatalf("payment closed %d times; the met obligation must not be redone", h.pay.closed)
	}
}

// An open under an id that is in flight with DIFFERENT content is a
// reuse, refused as such, not told to retry; and a failed secret or
// stage write leaves no reservation behind.
func TestInFlightOpenWithDifferentContentIsReuse(t *testing.T) {
	h := newHarness(t)
	h.pay.openDelay = 200 * time.Millisecond
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = h.engine.Open(context.Background(), OpenRequest{RequestID: "req-dup", GatewaySessionID: "g1",
			SessionParams: json.RawMessage(`{"a":1}`), PaymentBytes: []byte{1}, AuthorizationBytes: h.authorization(t, "auth-dup", "req-dup", "g1", 100), InitialReservationWei: big.NewInt(50), Spec: h.spec, CapacityRef: "s1"})
	}()
	time.Sleep(50 * time.Millisecond)
	_, err := h.engine.Open(context.Background(), OpenRequest{RequestID: "req-dup", GatewaySessionID: "g2",
		SessionParams: json.RawMessage(`{"a":2}`), PaymentBytes: []byte{1}, AuthorizationBytes: h.authorization(t, "auth-dup-2", "req-dup", "g2", 100), InitialReservationWei: big.NewInt(50), Spec: h.spec, CapacityRef: "s2"})
	var pe *ProtocolError
	if !errors.As(err, &pe) || pe.Code != "request_id_reuse" {
		t.Fatalf("different content under an in-flight id: %v, want request_id_reuse", err)
	}
	<-done
	if h.pay.openCalls != 1 {
		t.Fatalf("payment opened %d times", h.pay.openCalls)
	}
}
