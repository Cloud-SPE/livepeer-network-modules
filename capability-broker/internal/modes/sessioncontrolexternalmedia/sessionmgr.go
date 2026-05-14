// Package sessioncontrolexternalmedia implements the
// session-control-external-media@v0 interaction-mode driver per
// livepeer-network-protocol/modes/session-control-external-media.md
// (plan 0026).
//
// The driver owns three surfaces:
//
//   - session-open POST at /v1/cap (handled by Driver.Serve)
//   - lifecycle-only control WebSocket at /v1/cap/{session_id}/control
//     (handled by Driver.ServeControlWS)
//   - reverse proxy at /_scope/{session_id}/* forwarding to the
//     workload backend's HTTP API (handled by Driver.ServeProxy)
//
// Unlike session-control-plus-media@v0, this mode does NOT relay media
// bytes — the workload backend (e.g. Daydream Scope) owns its own
// WebRTC plane and uses an external TURN provider (Cloudflare). The
// broker never sees media frames.
package sessioncontrolexternalmedia

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
)

// SessionRecord is the per-session state the broker keeps from
// session-open through teardown. Goroutine-safe; mutated by the
// control-WS handler, the proxy short-circuit, and the close path.
type SessionRecord struct {
	SessionID    string
	CapabilityID string
	OfferingID   string

	// BackendURL is the resolved upstream URL the /_scope proxy forwards
	// to. Copied from the capability's `backend.url` at session-open.
	BackendURL string

	// SessionStartPath and SessionStopPath are capability-declared paths
	// on the workload backend that begin/end a backend-side session.
	// The proxy short-circuits these to start the seconds-elapsed clock
	// and to drive terminal teardown.
	SessionStartPath string
	SessionStopPath  string

	OpenedAt  time.Time
	ExpiresAt time.Time

	// startedAt is set the first time the proxy sees a request to
	// SessionStartPath. Zero until then; used to defer the seconds-
	// elapsed clock until the gateway actually uses the GPU.
	mu        sync.Mutex
	startedAt time.Time
	closed    bool
	active    int32 // atomic; control-WS attached

	// outbound is the control-WS writer's outbound channel; populated
	// when a control-WS attaches, cleared on detach.
	outbound chan<- outboundEvent

	// LiveCounter is the running unit total polled by the payment
	// middleware. Populated by Driver.Serve at session-open from the
	// configured extractor.
	LiveCounter extractors.LiveCounter

	// Cancel tears down all per-session goroutines. Driver-owned.
	Cancel context.CancelFunc
}

// outboundEvent is a server-emitted control-WS frame plus its
// monotonic sequence number.
type outboundEvent struct {
	Seq  uint64
	Body []byte
}

// MarkStarted records the moment the proxy first saw the
// session-start path. Idempotent; subsequent calls are no-ops.
func (r *SessionRecord) MarkStarted(now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.startedAt.IsZero() {
		return false
	}
	r.startedAt = now
	return true
}

// StartedAt returns the proxy-first-contact timestamp, or zero if the
// proxy has not yet seen a session-start request.
func (r *SessionRecord) StartedAt() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.startedAt
}

// MarkClosed flips the closed flag and reports its prior value.
// Idempotent.
func (r *SessionRecord) MarkClosed() (wasClosed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	prior := r.closed
	r.closed = true
	return prior
}

// Closed reports whether teardown has begun.
func (r *SessionRecord) Closed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

// SetActive marks the record attached/detached from a control-WS.
func (r *SessionRecord) SetActive(v bool) {
	if v {
		atomic.StoreInt32(&r.active, 1)
	} else {
		atomic.StoreInt32(&r.active, 0)
	}
}

// IsActive reports whether a control-WS is currently attached.
func (r *SessionRecord) IsActive() bool {
	return atomic.LoadInt32(&r.active) == 1
}

// SetOutbound publishes the outbound writer channel. Called by the
// control-WS handler on upgrade.
func (r *SessionRecord) SetOutbound(ch chan<- outboundEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outbound = ch
}

// ClearOutbound releases the outbound writer channel. Called on
// control-WS detach.
func (r *SessionRecord) ClearOutbound() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outbound = nil
}

// Emit publishes a server-emitted event to the active control-WS
// writer, if one is attached. Drops silently when no WS is attached
// (the gateway will reconnect with last-seq replay in a follow-up plan).
func (r *SessionRecord) Emit(ev outboundEvent) {
	r.mu.Lock()
	out := r.outbound
	r.mu.Unlock()
	if out == nil {
		return
	}
	select {
	case out <- ev:
	default:
		// Slow consumer; drop. v0 lifecycle frames are not
		// load-bearing across reconnects.
	}
}

// Store is the in-memory session table. Process-scoped; broker restart
// drops every in-flight session (matching sessioncontrolplusmedia's
// behavior — see plan 0011-followup).
type Store struct {
	mu       sync.Mutex
	sessions map[string]*SessionRecord
}

// NewStore returns an empty store.
func NewStore() *Store {
	return &Store{sessions: make(map[string]*SessionRecord)}
}

// Add registers a new session. Returns an error if the id collides
// (caller-side bug — IDs are 12 random bytes).
func (s *Store) Add(rec *SessionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[rec.SessionID]; ok {
		return errors.New("session id already exists")
	}
	s.sessions[rec.SessionID] = rec
	return nil
}

// Get returns the session record by id, or nil if absent or closed.
func (s *Store) Get(id string) *SessionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.sessions[id]
	if !ok {
		return nil
	}
	if rec.Closed() {
		return nil
	}
	return rec
}

// Remove deletes the session by id. Idempotent.
func (s *Store) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

// Snapshot copies the current set of sessions for read-only iteration
// (used by watchdog goroutines so they don't hold the mutex during
// their work).
func (s *Store) Snapshot() []*SessionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*SessionRecord, 0, len(s.sessions))
	for _, r := range s.sessions {
		out = append(out, r)
	}
	return out
}
