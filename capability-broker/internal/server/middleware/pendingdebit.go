package middleware

import (
	"context"
	"math/big"
	"sync"
)

// PendingDebit is a debit that did not land, handed up to the job
// idempotency layer so the exchange can be recorded as delivered but
// unsettled rather than as settled-and-wrong.
//
// It is passed through the request context rather than returned, because
// the layer that discovers the failure (payment) sits inside the layer
// that owns the durable job record (idempotency), and the response has
// usually been written by the time the debit runs.
type PendingDebit struct {
	AuthorizationBytes []byte
	Sender             []byte
	WorkID             string
	DebitSeq           uint64

	// Inputs for completing authorization settlement after an uncertain
	// daemon response. The reservation remains encumbered until the
	// idempotent settlement operation returns an authoritative result.
	ReservedValueWei  *big.Int
	AccountFundingWei *big.Int
	AccountVersion    uint64
	ActualUnits       uint64
	MeasuredUnits     uint64
	WorkUnitName      string
	JobID             string
	RequestID         string
}

// PendingDebitSlot is a one-shot holder the outer layer installs and the
// payment layer fills.
type PendingDebitSlot struct {
	mu sync.Mutex
	p  *PendingDebit
}

// Set records the pending debit. Last write wins; there is one final
// debit per exchange.
func (s *PendingDebitSlot) Set(p *PendingDebit) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.p = p
}

// Get returns the pending debit, or nil when the exchange settled.
func (s *PendingDebitSlot) Get() *PendingDebit {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.p
}

type pendingDebitKey struct{}

// WithPendingDebitSlot installs a slot on ctx and returns both.
func WithPendingDebitSlot(ctx context.Context) (context.Context, *PendingDebitSlot) {
	slot := &PendingDebitSlot{}
	return context.WithValue(ctx, pendingDebitKey{}, slot), slot
}

// PendingDebitSlotFrom returns the installed slot, or nil when the
// caller is not running under a job idempotency layer (the session
// protocol, or a unit test exercising the payment middleware alone).
func PendingDebitSlotFrom(ctx context.Context) *PendingDebitSlot {
	slot, _ := ctx.Value(pendingDebitKey{}).(*PendingDebitSlot)
	return slot
}
