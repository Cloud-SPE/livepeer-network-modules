package payment

import (
	"context"
	"crypto/sha256"
	"errors"
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
}

type mockSession struct {
	workID              string
	sender              []byte
	capability          string
	offering            string
	pricePerWorkUnitWei *big.Int
	workUnit            string
	balance             *big.Int
	openedAt            time.Time
	closedAt            time.Time
	closed              bool
	debits              []int64 // for test inspection
}

// NewMock returns an empty Mock client.
func NewMock() *Mock {
	return &Mock{
		sessions: map[string]*mockSession{},
		debits:   map[string]int64{},
	}
}

func (m *Mock) GetTicketParams(_ context.Context, req GetTicketParamsRequest) (*TicketParams, error) {
	faceValue := new(big.Int)
	if req.FaceValue != nil {
		faceValue.Set(req.FaceValue)
	}
	return &TicketParams{
		Recipient:         append([]byte(nil), req.Recipient...),
		FaceValue:         faceValue,
		WinProb:           big.NewInt(0),
		RecipientRandHash: func() []byte { sum := sha256.Sum256(req.Recipient); return sum[:] }(),
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
	if _, exists := m.sessions[req.WorkID]; exists {
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
	sess, ok := m.sessions[req.WorkID]
	if !ok {
		return nil, errors.New("no session for work_id; OpenSession first")
	}
	// Mock seals the sender to a derived stub value if it isn't already
	// sealed. Real receivers extract sender from the wire Payment; the
	// mock is only used in unit tests and the broker smoke (where the
	// payment_bytes is also a stub).
	if len(sess.sender) == 0 {
		sess.sender = stubSenderFromPayment(req.PaymentBytes)
	}
	return &ProcessPaymentResult{
		Sender:  append([]byte(nil), sess.sender...),
		Balance: new(big.Int).Set(sess.balance),
	}, nil
}

func (m *Mock) DebitBalance(_ context.Context, req DebitBalanceRequest) (*big.Int, error) {
	if len(req.Sender) == 0 || req.WorkID == "" {
		return nil, errors.New("sender and work_id are required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[req.WorkID]
	if !ok {
		return nil, errors.New("session not found")
	}
	if sess.closed {
		return nil, errors.New("session is closed")
	}
	if !bytesEqual(sess.sender, req.Sender) {
		return nil, errors.New("sender mismatch")
	}
	seqKey := compositeSeq(req.Sender, req.WorkID, req.DebitSeq)
	if _, alreadyDebited := m.debits[seqKey]; alreadyDebited {
		return new(big.Int).Set(sess.balance), nil
	}
	debitWei := new(big.Int).Mul(sess.pricePerWorkUnitWei, big.NewInt(req.WorkUnits))
	sess.balance.Sub(sess.balance, debitWei)
	sess.debits = append(sess.debits, req.WorkUnits)
	m.debits[seqKey] = req.WorkUnits
	return new(big.Int).Set(sess.balance), nil
}

// SufficientBalance reports whether the session balance covers
// `min_work_units` of additional work at the session's price. The mock
// uses the same arithmetic as the real daemon: balance ≥ price ×
// min_work_units. Closed sessions always report insufficient.
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
	required := new(big.Int).Mul(sess.pricePerWorkUnitWei, big.NewInt(req.MinWorkUnits))
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
func stubSenderFromPayment(bytes []byte) []byte {
	out := make([]byte, 20)
	for i, b := range bytes {
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
