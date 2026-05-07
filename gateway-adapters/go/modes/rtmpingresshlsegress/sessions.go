// Package rtmpingresshlsegress is the gateway-side adapter for the
// rtmp-ingress-hls-egress@v0 interaction mode. It hosts an RTMP
// listener, accepts customer pushes keyed by `session_id` (with an
// optional stream-key check), and relays the FLV byte stream to the
// broker's RTMP ingest URL returned by the session-open response.
//
// HLS playback is NOT proxied by the gateway — the customer's player
// connects to the broker-issued `hls_playback_url` directly per the
// spec.
package rtmpingresshlsegress

import (
	"errors"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Session is one open RTMP-ingest session the gateway is mediating.
// The gateway calls the broker's POST /v1/cap session-open out-of-band
// (HTTP-reqresp shape; the TS half hosts the open path) and registers
// the resulting URLs here so the RTMP listener can route the customer
// push to the right broker.
type Session struct {
	// SessionID echoes the broker's `session_id`. The customer pushes to
	// `<RTMP_LISTEN_ADDR>/live/<SessionID>` (or `<SessionID>/<StreamKey>`
	// if `StreamKey` is non-empty).
	SessionID string

	// StreamKey is an optional shared secret the gateway hands to the
	// customer. When set, the customer's publishing name MUST be
	// `<SessionID>/<StreamKey>`; mismatches are rejected.
	StreamKey string

	// RTMPIngestURL is the upstream URL the broker returned in its
	// session-open response (e.g.
	// `rtmp://broker.example.com:1935/live/sess_xyz`). The adapter
	// dials this on customer push and relays bytes here.
	RTMPIngestURL string

	// ExpiresAt is the broker-assigned deadline for the customer's
	// initial push. Past that, the session is auto-closed by the
	// broker; the adapter cleans up its slot proactively if its watcher
	// hits the boundary first.
	ExpiresAt time.Time

	// Started is the wall-clock time the customer's push first reached
	// the listener. Zero until the first FLV byte arrives.
	Started time.Time
}

// Sessions is a thread-safe map of open RTMP sessions.
type Sessions struct {
	mu    sync.RWMutex
	store map[string]*Session
}

// NewSessions returns an empty Sessions registry.
func NewSessions() *Sessions {
	return &Sessions{store: map[string]*Session{}}
}

// Register adds a session. Returns an error if the SessionID is already
// in use.
func (s *Sessions) Register(sess *Session) error {
	if sess == nil || sess.SessionID == "" {
		return errors.New("session: missing SessionID")
	}
	if sess.RTMPIngestURL == "" {
		return errors.New("session: missing RTMPIngestURL")
	}
	if !strings.HasPrefix(sess.RTMPIngestURL, "rtmp://") && !strings.HasPrefix(sess.RTMPIngestURL, "rtmps://") {
		return errors.New("session: RTMPIngestURL must be rtmp:// or rtmps://")
	}
	if _, err := url.Parse(sess.RTMPIngestURL); err != nil {
		return errors.New("session: RTMPIngestURL parse: " + err.Error())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.store[sess.SessionID]; exists {
		return errors.New("session: SessionID already registered")
	}
	s.store[sess.SessionID] = sess
	return nil
}

// Lookup returns the session keyed by SessionID, or nil if absent.
func (s *Sessions) Lookup(sessionID string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.store[sessionID]
}

// Remove drops a session by SessionID. No-op if absent.
func (s *Sessions) Remove(sessionID string) {
	s.mu.Lock()
	delete(s.store, sessionID)
	s.mu.Unlock()
}

// Active returns the count of registered sessions.
func (s *Sessions) Active() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.store)
}

// MarkStarted stamps the session's Started time on first FLV byte.
// Idempotent.
func (s *Sessions) MarkStarted(sessionID string, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.store[sessionID]
	if sess != nil && sess.Started.IsZero() {
		sess.Started = t
	}
}
