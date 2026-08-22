package payment

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
	"math/big"
	"sync"
	"time"
)

// Mock is an in-process Client used by unit tests and the broker
// standalone smoke. Sessions live in memory; ProcessPayment seals the
// sender on first call; DebitBalance is idempotent by debit_seq.
type Mock struct {
	mu       sync.Mutex
	sessions map[string]*mockSession // keyed by work_id (sender unsealed) then composite (sender||work_id)
	debits   map[string]int64        // (sender||work_id||seq) → recorded units
	// statePath, when set via EnablePersistence, makes the ledger
	// survive the process (see mock_persist.go).
	statePath string

	// creditOverride replaces the default per-payment credit when a test
	// needs a payment that funds nothing.
	creditOverride *big.Int

	// failDebits counts down injected DebitBalance failures, so a test
	// can exercise work shipping while the ledger call does not land.
	failDebits int

	// rejectPayments counts down injected full-batch rejections.
	rejectPayments int
	rejectReason   PaymentRejectionReason
}

type mockSession struct {
	workID              string
	sender              []byte
	capability          string
	offering            string
	pricePerWorkUnitWei *big.Int
	perUnits            uint64
	workUnit            string
	balance             *big.Int
	debitedUnits        uint64 // cumulative, for the billing rule
	openedAt            time.Time
	closedAt            time.Time
	closed              bool
	debits              []int64       // for test inspection
	debitLog            []DebitRecord // (seq, units) applied, in order
}

// NewMock returns an empty Mock client.
func NewMock() *Mock {
	return &Mock{
		sessions: map[string]*mockSession{},
		debits:   map[string]int64{},
	}
}

// ErrPricingConflict mirrors the real ledger's refusal to re-price a
// session an offering already priced.
var ErrPricingConflict = errors.New("payment: session price already set by an offering and differs")

// ErrPricingUnset mirrors the real ledger's refusal to bill a session no
// offering has priced. Treating unset as zero is how work gets served
// free while every log line reports success.
var ErrPricingUnset = errors.New("payment: session has no offering price; refusing to bill")

// GetTicketParams mints ticket params AND creates the payment session —
// unpriced — exactly as the real payee does.
//
// This ordering is the whole point. A sender cannot mint without params,
// so this call always runs FIRST and the session exists before any
// offering has priced it. The mock used to create nothing here, which
// meant the sequence that let every exchange bill zero in production
// could not be represented in a test at all.
func (m *Mock) GetTicketParams(_ context.Context, req GetTicketParamsRequest) (*TicketParams, error) {
	faceValue := new(big.Int)
	if req.FaceValue != nil {
		faceValue.Set(req.FaceValue)
	}
	randHash := sha256.Sum256(append(append([]byte(nil), req.Sender...), req.Recipient...))
	workID := hex.EncodeToString(randHash[:])

	m.mu.Lock()
	if _, exists := m.sessions[workID]; !exists {
		m.sessions[workID] = &mockSession{
			workID: workID,
			// Unpriced: this call knows nothing about what work costs.
			pricePerWorkUnitWei: nil,
			balance:             new(big.Int),
			openedAt:            time.Now(),
		}
		m.flushLocked()
	}
	m.mu.Unlock()

	return &TicketParams{
		Recipient:         append([]byte(nil), req.Recipient...),
		FaceValue:         faceValue,
		WinProb:           big.NewInt(0),
		RecipientRandHash: randHash[:],
		Seed:              nil,
		ExpirationBlock:   new(big.Int),
	}, nil
}

func (m *Mock) OpenSession(_ context.Context, req OpenSessionRequest) (*OpenSessionResult, error) {
	if req.WorkID == "" {
		return nil, errors.New("work_id is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	defer m.flushLocked()
	if existing, exists := m.sessions[req.WorkID]; exists {
		// Apply the offering's pricing exactly once, and refuse to move
		// it afterwards — the same rule the real ledger enforces.
		switch {
		case existing.pricePerWorkUnitWei == nil:
			price := req.PricePerWorkUnitWei
			if price == nil {
				price = new(big.Int)
			}
			existing.pricePerWorkUnitWei = new(big.Int).Set(price)
			existing.perUnits = req.PerUnits
			existing.workUnit = req.WorkUnit
			existing.capability = req.Capability
			existing.offering = req.Offering
		case req.PricePerWorkUnitWei != nil &&
			(existing.pricePerWorkUnitWei.Cmp(req.PricePerWorkUnitWei) != 0 || existing.perUnits != req.PerUnits):
			return nil, ErrPricingConflict
		}
		return &OpenSessionResult{AlreadyOpen: true}, nil
	}
	price := req.PricePerWorkUnitWei
	if price == nil {
		price = new(big.Int)
	}
	m.sessions[req.WorkID] = &mockSession{
		workID:              req.WorkID,
		capability:          req.Capability,
		offering:            req.Offering,
		pricePerWorkUnitWei: new(big.Int).Set(price),
		perUnits:            req.PerUnits,
		workUnit:            req.WorkUnit,
		balance:             new(big.Int),
		openedAt:            time.Now(),
	}
	return &OpenSessionResult{AlreadyOpen: false}, nil
}

func (m *Mock) ProcessPayment(_ context.Context, req ProcessPaymentRequest) (*ProcessPaymentResult, error) {
	if req.WorkID == "" {
		return nil, errors.New("work_id is empty")
	}
	if len(req.PaymentBytes) == 0 {
		return nil, errors.New("payment_bytes is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	defer m.flushLocked()
	sess, ok := m.sessions[req.WorkID]
	if !ok {
		return nil, errors.New("no session for work_id; OpenSession first")
	}
	// A closed session takes no more money — same refusal, same in-band
	// signal, as the real payee. Without this the mock credited a
	// rotated-away session while every debit against it failed, so a
	// broker that stranded a payer's funds on a dead identity passed
	// every mock-backed test.
	if sess.closed {
		return &ProcessPaymentResult{
			Sender:     append([]byte(nil), sess.sender...),
			CreditedEV: big.NewInt(0),
			Balance:    new(big.Int).Set(sess.balance),
			// The status list matters: the broker only treats a batch as
			// fully rejected when tickets_rejected covers every entry,
			// so a bare count with no statuses reads as accepted.
			TicketStatus: []TicketStatus{{
				RejectionReason: PaymentRejectionReasonInvalidRecipientRand,
			}},
			TicketsRejected:   1,
			DominantRejection: PaymentRejectionReasonInvalidRecipientRand,
		}, nil
	}

	// Mock seals the sender to a derived stub value if it isn't already
	// sealed. Real receivers extract sender from the wire Payment; the
	// mock is only used in unit tests and the broker smoke (where the
	// payment_bytes is also a stub).
	if len(sess.sender) == 0 {
		sess.sender = stubSenderFromPayment(req.PaymentBytes)
	}
	// Cross-check the price against what the SENDER signed, as the real
	// payee does. Without this a broker could bill at a rate the gateway
	// never agreed to and every test would still pass.
	if sess.pricePerWorkUnitWei != nil {
		var pay pb.Payment
		if err := proto.Unmarshal(req.PaymentBytes, &pay); err == nil {
			if ep := pay.GetExpectedPrice(); ep != nil {
				if big.NewInt(ep.GetPricePerUnit()).Cmp(sess.pricePerWorkUnitWei) != 0 {
					return nil, fmt.Errorf("payment signed price %d does not match the session price %s",
						ep.GetPricePerUnit(), sess.pricePerWorkUnitWei)
				}
			}
		}
	}

	if m.rejectPayments > 0 {
		m.rejectPayments--
		return &ProcessPaymentResult{
			Sender:     append([]byte(nil), sess.sender...),
			CreditedEV: big.NewInt(0),
			Balance:    new(big.Int).Set(sess.balance),
			TicketStatus: []TicketStatus{{
				RejectionReason: m.rejectReason,
			}},
			TicketsRejected:   1,
			DominantRejection: m.rejectReason,
		}, nil
	}

	// Credit the session. The mock used to accept a payment and credit
	// NOTHING, so every mock-backed test, conformance run and dev
	// deployment served work against a zero balance — which is exactly
	// how the real zero-billing defect stayed invisible. A payment that
	// credits nothing is not a payment.
	//
	// A test that wants an exhausted session sets the balance directly.
	credited := new(big.Int).Set(m.creditPerPayment())
	sess.balance.Add(sess.balance, credited)
	return &ProcessPaymentResult{
		Sender:     append([]byte(nil), sess.sender...),
		CreditedEV: credited,
		Balance:    new(big.Int).Set(sess.balance),
	}, nil
}

// mockCreditPerPayment is what one stub payment funds: 0.001 ETH, which
// is generous against the wei-scale prices fixtures use and keeps the
// mock from being the thing that fails a test about something else.
var mockCreditPerPayment = new(big.Int).Exp(big.NewInt(10), big.NewInt(15), nil)

// RejectNextPayments makes the next n ProcessPayment calls report every
// ticket rejected for the given reason, crediting nothing — the shape a
// replayed nonce stream or a bad signature produces.
func (m *Mock) RejectNextPayments(n int, reason PaymentRejectionReason) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rejectPayments = n
	m.rejectReason = reason
}

// FailNextDebits makes the next n DebitBalance calls fail, so a test can
// exercise the path where work ships and the ledger call does not land.
// Without it the durable-retry lifecycle is unreachable from a test, and
// that lifecycle is the one that decides whether a delivered exchange
// ever settles.
func (m *Mock) FailNextDebits(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failDebits = n
}

func (m *Mock) DebitBalance(_ context.Context, req DebitBalanceRequest) (*DebitResult, error) {
	if len(req.Sender) == 0 || req.WorkID == "" {
		return nil, errors.New("sender and work_id are required")
	}
	m.mu.Lock()
	if m.failDebits > 0 {
		m.failDebits--
		m.mu.Unlock()
		return nil, errors.New("mock: injected debit failure")
	}
	m.mu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	defer m.flushLocked()
	sess, ok := m.sessions[req.WorkID]
	if !ok {
		return nil, errors.New("session not found")
	}
	if sess.closed {
		return nil, errors.New("session is closed")
	}
	if sess.pricePerWorkUnitWei == nil {
		return nil, ErrPricingUnset
	}
	if !bytesEqual(sess.sender, req.Sender) {
		return nil, errors.New("sender mismatch")
	}
	seqKey := compositeSeq(req.Sender, req.WorkID, req.DebitSeq)
	if _, alreadyDebited := m.debits[seqKey]; alreadyDebited {
		return &DebitResult{
			Balance:         new(big.Int).Set(sess.balance),
			DebitedWei:      new(big.Int),
			CumulativeUnits: sess.debitedUnits,
			Replayed:        true,
		}, nil
	}
	// Cumulative ceiling billing, same rule as the real daemon
	// (offering-axes.md §6.1). A mock that priced each debit on its own
	// would let dev and conformance runs disagree with production about
	// what a session cost.
	before := BillFor(sess.pricePerWorkUnitWei, sess.perUnits, sess.debitedUnits)
	sess.debitedUnits += uint64(req.WorkUnits)
	debitWei := new(big.Int).Sub(
		BillFor(sess.pricePerWorkUnitWei, sess.perUnits, sess.debitedUnits), before)
	sess.balance.Sub(sess.balance, debitWei)
	sess.debits = append(sess.debits, req.WorkUnits)
	sess.debitLog = append(sess.debitLog, DebitRecord{
		Seq: req.DebitSeq, Units: req.WorkUnits, Wei: new(big.Int).Set(debitWei)})
	m.debits[seqKey] = req.WorkUnits
	return &DebitResult{
		Balance:         new(big.Int).Set(sess.balance),
		DebitedWei:      debitWei,
		CumulativeUnits: sess.debitedUnits,
		Replayed:        false,
	}, nil
}

// SufficientBalance reports whether the session balance covers
// `min_work_units` of additional work at the session's price. The mock
// uses the same arithmetic as the real daemon: the balance must cover
// the difference between cumulative bills. Closed sessions always report
// insufficient.
func (m *Mock) SufficientBalance(_ context.Context, req SufficientBalanceRequest) (*SufficientBalanceResult, error) {
	if len(req.Sender) == 0 || req.WorkID == "" {
		return nil, errors.New("sender and work_id are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[req.WorkID]
	if !ok {
		return nil, errors.New("session not found")
	}
	if !bytesEqual(sess.sender, req.Sender) {
		return nil, errors.New("sender mismatch")
	}
	if sess.closed {
		return &SufficientBalanceResult{
			Sufficient: false,
			Balance:    new(big.Int).Set(sess.balance),
		}, nil
	}
	min := req.MinWorkUnits
	if min < 0 {
		min = 0
	}
	required := new(big.Int).Sub(
		BillFor(sess.pricePerWorkUnitWei, sess.perUnits, sess.debitedUnits+uint64(min)),
		BillFor(sess.pricePerWorkUnitWei, sess.perUnits, sess.debitedUnits))
	sufficient := sess.balance.Cmp(required) >= 0
	return &SufficientBalanceResult{
		Sufficient: sufficient,
		Balance:    new(big.Int).Set(sess.balance),
	}, nil
}

// GetBalance returns the current balance for a (sender, work_id) pair.
func (m *Mock) GetBalance(_ context.Context, sender []byte, workID string) (*big.Int, error) {
	if len(sender) == 0 || workID == "" {
		return nil, errors.New("sender and work_id are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[workID]
	if !ok {
		return nil, errors.New("session not found")
	}
	if !bytesEqual(sess.sender, sender) {
		return nil, errors.New("sender mismatch")
	}
	return new(big.Int).Set(sess.balance), nil
}

func (m *Mock) CloseSession(_ context.Context, sender []byte, workID string) error {
	if workID == "" {
		return errors.New("work_id is empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	defer m.flushLocked()
	sess, ok := m.sessions[workID]
	if !ok {
		return errors.New("session not found")
	}
	if !bytesEqual(sess.sender, sender) {
		return errors.New("sender mismatch")
	}
	sess.closed = true
	sess.closedAt = time.Now()
	return nil
}

// CreditBalance is a test helper that adds `wei` to a session's balance.
// Used by unit tests and the conformance fixture to seed runway without
// going through ProcessPayment.
// DebitRecord is one applied debit, for tests that need to see which
// sequence numbers were actually used rather than only the total.
type DebitRecord struct {
	Seq   uint64
	Units int64
	// Wei is what this debit actually charged under cumulative billing.
	Wei *big.Int
}

// Debits returns the debits APPLIED to a work_id — deduplicated repeats
// are absent, which is the point: a test asserting that N exchanges
// billed N times must see the drops.
func (m *Mock) Debits(workID string) []DebitRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[workID]
	if !ok {
		return nil
	}
	return append([]DebitRecord(nil), sess.debitLog...)
}

// SetCreditPerPayment overrides what one payment credits. Zero models a
// payment whose expected value rounds away — the case a mainnet run
// actually produced, where a valid ticket credited nothing and the
// broker served work anyway.
func (m *Mock) SetCreditPerPayment(wei *big.Int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.creditOverride = new(big.Int).Set(wei)
}

func (m *Mock) creditPerPayment() *big.Int {
	if m.creditOverride != nil {
		return m.creditOverride
	}
	return mockCreditPerPayment
}

// SetBalance forces a session's balance, for tests that need an
// exhausted or precisely-funded session. Stating the precondition beats
// relying on the mock's own crediting behaviour, which is what the
// insufficient-balance test used to do — and which silently stopped
// testing anything the moment the mock began crediting.
func (m *Mock) SetBalance(workID string, wei *big.Int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer m.flushLocked()
	sess, ok := m.sessions[workID]
	if !ok {
		return errors.New("no session for work_id")
	}
	sess.balance = new(big.Int).Set(wei)
	return nil
}

func (m *Mock) CreditBalance(workID string, wei *big.Int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[workID]
	if !ok {
		return errors.New("session not found")
	}
	sess.balance.Add(sess.balance, wei)
	return nil
}

// SessionRecord is a snapshot of one mock session for test inspection.
type SessionRecord struct {
	WorkID     string
	Sender     []byte
	Capability string
	Offering   string
	OpenedAt   time.Time
	ClosedAt   time.Time
	Debits     []int64
	Balance    *big.Int
	Closed     bool
}

// Sessions returns a snapshot of all recorded mock sessions.
func (m *Mock) Sessions() []SessionRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SessionRecord, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, SessionRecord{
			WorkID:     s.workID,
			Sender:     append([]byte(nil), s.sender...),
			Capability: s.capability,
			Offering:   s.offering,
			OpenedAt:   s.openedAt,
			ClosedAt:   s.closedAt,
			Debits:     append([]int64(nil), s.debits...),
			Balance:    new(big.Int).Set(s.balance),
			Closed:     s.closed,
		})
	}
	return out
}

func compositeSeq(sender []byte, workID string, seq uint64) string {
	out := make([]byte, 0, len(sender)+1+len(workID)+9)
	out = append(out, sender...)
	out = append(out, ':')
	out = append(out, []byte(workID)...)
	for i := 0; i < 8; i++ {
		out = append(out, byte(seq>>(8*i)))
	}
	return string(out)
}

// stubSenderFromPayment derives a deterministic 20-byte "sender" from
// the payment_bytes for mock-mode use. This is NOT a real recovery; it
// just gives the mock a stable identity to seal the session against.
// stubSenderFromPayment recovers the sender the way a real payee does:
// from the payment's own sender field.
//
// It used to XOR the whole payment, which made "same wallet, new ticket
// params" look like a DIFFERENT wallet — exactly the recipient-rotation
// case, where a rebind is then refused for a sender mismatch that does
// not exist. A mock that cannot represent the scenario under test turns
// a passing suite into no evidence at all.
//
// Opaque stub bytes have no sender field, so they keep the old
// derivation: stable per payment, which is all those tests need.
func stubSenderFromPayment(raw []byte) []byte {
	var pay pb.Payment
	if err := proto.Unmarshal(raw, &pay); err == nil && len(pay.GetSender()) > 0 {
		return append([]byte(nil), pay.GetSender()...)
	}
	out := make([]byte, 20)
	for i, b := range raw {
		out[i%20] ^= b
	}
	return out
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Compile-time interface check.
var _ Client = (*Mock)(nil)
