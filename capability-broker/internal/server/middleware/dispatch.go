package middleware

import (
	"context"
	"sync"
	"time"
)

// Dispatch is the fact that an exchange was sent to a runner, and to
// which one. It exists so that a backend outcome can be reported once
// per exchange, from the one place that already classifies every
// exchange for every transport — the job idempotency layer — rather
// than from each of the terminal returns inside handleJob.
//
// It rides the request context for the same reason PendingDebit does:
// the layer that knows the runner (dispatch) sits inside the layer that
// knows the outcome (idempotency), and the response has usually been
// written by the time the classification runs.
type Dispatch struct {
	// BackendID is the broker's own id for the runner: host|local.
	BackendID    string
	HostID       string
	LocalID      string
	CapabilityID string
	OfferingID   string
	// DispatchedAt is when the request left for the runner; FirstByteAt
	// is when its response headers arrived. Their difference is the one
	// latency reported for every transport (plan 0045 §2): a stream's
	// total duration measures how much the caller asked for, not how
	// fast the runner is.
	DispatchedAt time.Time
	FirstByteAt  time.Time
	// Forwarded is false when the runner never answered at all — the
	// tunnel failed, or the read did — which is a backend failure with
	// no latency worth the name.
	Forwarded bool
}

// DispatchSlot is a one-shot holder the idempotency layer installs and
// handleJob fills once it has selected a runner.
type DispatchSlot struct {
	mu sync.Mutex
	d  *Dispatch
}

// Set records the dispatch. Last write wins; there is one per exchange.
func (s *DispatchSlot) Set(d *Dispatch) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d = d
}

// Get returns the dispatch, or nil when the exchange was refused before
// any runner was chosen — which is exactly the case that must NOT be
// reported: the broker's own refusal is not the runner's outcome.
func (s *DispatchSlot) Get() *Dispatch {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.d
}

type dispatchKey struct{}

// WithDispatchSlot installs a slot on ctx and returns both.
func WithDispatchSlot(ctx context.Context) (context.Context, *DispatchSlot) {
	slot := &DispatchSlot{}
	return context.WithValue(ctx, dispatchKey{}, slot), slot
}

// DispatchSlotFrom returns the installed slot, or nil outside the job
// idempotency layer.
func DispatchSlotFrom(ctx context.Context) *DispatchSlot {
	slot, _ := ctx.Value(dispatchKey{}).(*DispatchSlot)
	return slot
}
