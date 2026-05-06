package payment

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

// Mock is an in-process payment client for v0.1. It accepts any non-empty
// payment blob, generates session IDs, and records all lifecycle calls in
// memory for inspection by tests and smoke runs.
//
// Mock is goroutine-safe.
type Mock struct {
	mu       sync.Mutex
	sessions map[string]*mockSession
}

type mockSession struct {
	req         OpenSessionRequest
	openedAt    time.Time
	closedAt    time.Time
	debits      []uint64
	actualUnits *uint64
	closed      bool
}

// NewMock returns an empty Mock client.
func NewMock() *Mock {
	return &Mock{sessions: map[string]*mockSession{}}
}

// OpenSession assigns a fresh session ID and records the request blob.
// Returns an error only if the payment blob is empty (defensive; the Headers
// middleware should reject earlier).
func (m *Mock) OpenSession(ctx context.Context, req OpenSessionRequest) (*Session, error) {
	if req.PaymentBlob == "" {
		return nil, errors.New("payment blob is empty")
	}
	id := generateID()
	m.mu.Lock()
	m.sessions[id] = &mockSession{req: req, openedAt: time.Now()}
	m.mu.Unlock()
	return &Session{ID: id}, nil
}

// Debit appends an entry to the session's debit ledger.
func (m *Mock) Debit(ctx context.Context, sessionID string, units uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sessionID]
	if !ok {
		return errSessionNotFound(sessionID)
	}
	s.debits = append(s.debits, units)
	return nil
}

// Reconcile records the post-Serve actual units.
func (m *Mock) Reconcile(ctx context.Context, sessionID string, actualUnits uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sessionID]
	if !ok {
		return errSessionNotFound(sessionID)
	}
	s.actualUnits = &actualUnits
	return nil
}

// Close marks the session closed.
func (m *Mock) Close(ctx context.Context, sessionID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[sessionID]
	if !ok {
		return errSessionNotFound(sessionID)
	}
	s.closed = true
	s.closedAt = time.Now()
	return nil
}

// SessionRecord is a snapshot of one mock session's lifecycle for test
// inspection. Returned by Sessions().
type SessionRecord struct {
	ID          string
	Capability  string
	Offering    string
	OpenedAt    time.Time
	ClosedAt    time.Time
	Debits      []uint64
	ActualUnits *uint64
	Closed      bool
}

// Sessions returns a snapshot of all recorded sessions.
func (m *Mock) Sessions() []SessionRecord {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SessionRecord, 0, len(m.sessions))
	for id, s := range m.sessions {
		out = append(out, SessionRecord{
			ID:          id,
			Capability:  s.req.CapabilityID,
			Offering:    s.req.OfferingID,
			OpenedAt:    s.openedAt,
			ClosedAt:    s.closedAt,
			Debits:      append([]uint64(nil), s.debits...),
			ActualUnits: s.actualUnits,
			Closed:      s.closed,
		})
	}
	return out
}

func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

type errSessionNotFound string

func (e errSessionNotFound) Error() string {
	return "payment: session not found: " + string(e)
}

// Compile-time interface check.
var _ Client = (*Mock)(nil)
