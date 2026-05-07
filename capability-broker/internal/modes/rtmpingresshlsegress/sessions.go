package rtmpingresshlsegress

import (
	"crypto/subtle"
	"errors"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/extractors"
)

// SessionRecord is the in-memory state the broker keeps per open session.
// It is created by Serve when the session-open POST returns 202 and
// removed when any of the four termination triggers fires (see
// docs/exec-plans/active/0011-followup §7).
type SessionRecord struct {
	SessionID string
	StreamKey string

	// Profile is the resolved encoder profile from the capability's
	// host-config entry (e.g. "h264-live-1080p-libx264").
	Profile string

	// CapabilityID + OfferingID label the session for log lines.
	CapabilityID string
	OfferingID   string

	ExpiresAt time.Time
	OpenedAt  time.Time

	// Cancel terminates the per-session media goroutines: the
	// FFmpeg subprocess wrapper, the RTMP connection handler, and
	// the LL-HLS scratch cleanup. nil before the first successful
	// publish handshake.
	Cancel func()

	// LiveCounter is the running work-unit view the interim-debit
	// ticker polls. The mode driver populates this when the RTMP
	// publish handshake completes and the encoder is running.
	LiveCounter extractors.LiveCounter

	// Publishing is true after a successful publish handshake.
	Publishing bool

	// LastPacketAt is updated by the RTMP handler on every audio /
	// video tag. Read by the idle-timeout watchdog.
	LastPacketAt time.Time
}

// Store holds the broker's open RTMP sessions. v0.1 is in-memory and
// process-scoped; broker restart drops all in-flight RTMP sessions
// (matching payment-daemon's BoltDB behaviour for in-flight tickets).
type Store struct {
	mu       sync.Mutex
	sessions map[string]*SessionRecord
}

// NewStore returns an empty session store.
func NewStore() *Store {
	return &Store{sessions: make(map[string]*SessionRecord)}
}

// Add registers a new session. Returns an error if the id is already
// present (caller-side bug — IDs are 12 random bytes).
func (s *Store) Add(rec *SessionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[rec.SessionID]; ok {
		return errors.New("session id already exists")
	}
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

// Lookup performs a constant-time stream-key check and returns the
// record on match. Returns (nil, false) on missing session or key
// mismatch. Designed for the RTMP listener's OnPublish callback.
func (s *Store) Lookup(sessionID, streamKey string) (*SessionRecord, bool) {
	s.mu.Lock()
	rec, ok := s.sessions[sessionID]
	s.mu.Unlock()
	if !ok {
		return nil, false
	}
	if subtle.ConstantTimeCompare([]byte(rec.StreamKey), []byte(streamKey)) != 1 {
		return nil, false
	}
	return rec, true
}

// MarkPublishing flips the record's Publishing flag and seeds
// LastPacketAt. Returns the prior Publishing value so callers can
// implement reject/replace policy on duplicate publishes.
func (s *Store) MarkPublishing(id string, now time.Time) (alreadyPublishing bool, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, found := s.sessions[id]
	if !found {
		return false, false
	}
	prior := rec.Publishing
	rec.Publishing = true
	rec.LastPacketAt = now
	return prior, true
}

// AttachMedia binds the per-session encoder/cancel handles. Called by
// the listener-side wire-up (server package) after a successful
// publish handshake. Returns false when the session is unknown.
func (s *Store) AttachMedia(id string, lc extractors.LiveCounter, cancel func()) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	rec, ok := s.sessions[id]
	if !ok {
		return false
	}
	rec.LiveCounter = lc
	rec.Cancel = cancel
	return true
}

// LiveCounter returns the running counter for a session, or nil if
// none. The dispatch layer reads this to publish the LiveCounter to
// the interim-debit ticker.
func (s *Store) LiveCounter(id string) extractors.LiveCounter {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.sessions[id]; ok {
		return rec.LiveCounter
	}
	return nil
}

// Touch updates the last-packet timestamp. Called from the RTMP
// audio/video callback hot path.
func (s *Store) Touch(id string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.sessions[id]; ok {
		rec.LastPacketAt = now
	}
}

// Snapshot copies the current set of sessions for read-only iteration
// (used by watchdog goroutines to avoid holding the mutex during their
// work).
func (s *Store) Snapshot() []*SessionRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*SessionRecord, 0, len(s.sessions))
	for _, r := range s.sessions {
		out = append(out, r)
	}
	return out
}
