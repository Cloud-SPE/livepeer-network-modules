package sessioncontrolplusmedia

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
)

// Backend abstracts the session-runner subprocess + media-relay
// machinery the control-WS relays against. The session-open driver
// owns construction; the runner-backed implementation in
// runnerbackend.go wires the real session-runner subprocess.
//
// The control-WS path stays driver-state-only when no Backend is
// registered (Backend==nil): workload envelopes are silently dropped
// and the protocol-reserved short-circuits still fire. This is the
// loopback shape used by the unit tests.
type Backend interface {
	// AttachControl is invoked when a control-WS comes up for a
	// session. Implementations return their own Inbound/Outbound
	// channels for relayed envelopes; closure of the returned
	// "done" channel signals the runner exited.
	AttachControl(ctx context.Context, sessionID string) (BackendControl, error)

	// DetachControl notifies the runner side that the active
	// control-WS dropped (entering the reconnect window). The
	// runner subprocess itself stays alive.
	DetachControl(sessionID string, code int, reason string)

	// ReattachControl notifies the runner that the customer
	// reconnected within the window.
	ReattachControl(sessionID string)

	// Shutdown tears down the runner subprocess + IPC streams.
	// Invoked at full session teardown (clean session.end, runner
	// crash, reconnect-window expiry).
	Shutdown(sessionID string)
}

// BackendControl is the per-session handle the relay consumes.
// Inbound: customer → runner envelopes. Outbound: runner → customer
// envelopes.
type BackendControl struct {
	Inbound  chan<- ControlEnvelope
	Outbound <-chan ControlEnvelope
	Done     <-chan struct{}
	Cancel   context.CancelFunc
}

// SessionRecord is the per-session state the broker keeps. It is
// created at session-open (Driver.Serve) and lives until the session
// fully tears down.
type SessionRecord struct {
	SessionID string

	CapabilityID string
	OfferingID   string

	OpenedAt  time.Time
	ExpiresAt time.Time

	// active gates whether a control-WS is currently bound to this
	// session. Reconnect logic flips it on disconnect / reconnect.
	mu        sync.Mutex
	active    bool
	closing   bool
	disconnAt time.Time

	// nextSeq is the broker-monotonic seq applied to every
	// server-emitted envelope. Resumes across reconnects so the
	// client's Last-Seq pointer stays valid.
	nextSeq uint64

	// replay is the bounded buffer of server-emitted envelopes
	// retained for replay-on-reconnect. Sized by Driver config.
	replay *replayBuffer

	// Backend channels active for the bound control-WS. Nil when
	// no WS is currently attached.
	control *BackendControl

	// LiveCounter exposes the running unit total to the
	// interim-debit ticker. Populated by the mode driver at
	// session-open from the configured extractor.
	LiveCounter extractors.LiveCounter

	// Cancel tears down all per-session goroutines. Driver-owned;
	// invoked on full teardown.
	Cancel context.CancelFunc
}

// NextSeq returns the next outbound sequence number.
func (r *SessionRecord) NextSeq() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextSeq++
	return r.nextSeq
}

// SetActive marks the record bound / unbound to a control-WS.
func (r *SessionRecord) SetActive(v bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active = v
	if !v {
		r.disconnAt = time.Now()
	}
}

// IsActive reports whether a control-WS is currently bound.
func (r *SessionRecord) IsActive() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.active
}

// DisconnectedAt returns the wall-clock instant the WS dropped.
func (r *SessionRecord) DisconnectedAt() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.disconnAt
}

// markClosing flips the closing flag to true and reports the prior
// value. Idempotent.
func (r *SessionRecord) markClosing() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	prior := r.closing
	r.closing = true
	return prior
}

// Closing reports whether teardown is in progress.
func (r *SessionRecord) Closing() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closing
}

// Store is the in-memory session table. v0.1 is process-scoped;
// broker restart drops every in-flight session (matching plan
// 0011-followup's RTMP store behaviour).
type Store struct {
	mu       sync.Mutex
	sessions map[string]*SessionRecord
	cfg      StoreConfig
}

// StoreConfig governs the per-session bounded buffers.
type StoreConfig struct {
	// ReplayBufferMessages caps the replay buffer at message count.
	ReplayBufferMessages int
	// ReplayBufferBytes caps the replay buffer at total JSON byte
	// size. 0 disables the byte cap (count-only).
	ReplayBufferBytes int
}

// NewStore returns an empty store with the given limits.
func NewStore(cfg StoreConfig) *Store {
	if cfg.ReplayBufferMessages <= 0 {
		cfg.ReplayBufferMessages = 64
	}
	if cfg.ReplayBufferBytes < 0 {
		cfg.ReplayBufferBytes = 0
	}
	return &Store{
		sessions: make(map[string]*SessionRecord),
		cfg:      cfg,
	}
}

// Add registers a new session. Returns an error if the id already
// exists (caller-side bug — IDs are 12 random bytes).
func (s *Store) Add(rec *SessionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[rec.SessionID]; ok {
		return errors.New("session id already exists")
	}
	rec.replay = newReplayBuffer(s.cfg.ReplayBufferMessages, s.cfg.ReplayBufferBytes)
	s.sessions[rec.SessionID] = rec
	return nil
}

// Get returns the session record by id, or nil if absent.
func (s *Store) Get(id string) *SessionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
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
