package rtmpingresshlsegress

import (
	"context"
	"log"
	"time"
)

// LifetimeOptions configures the watchdog goroutines.
type LifetimeOptions struct {
	// IdleTimeout is the no-packet window after a successful publish
	// handshake before the session is torn down. Zero disables the
	// watchdog (used by tests).
	IdleTimeout time.Duration
	// CheckInterval drives the watchdog poll cadence. Zero defaults
	// to one second.
	CheckInterval time.Duration
}

// RunWatchdog starts background goroutines for the expires_at and
// idle-timeout triggers. Returns when ctx is canceled. The function
// blocks; callers run it on its own goroutine.
//
// Triggers 3 and 4 (SufficientBalance + customer CloseSession) live
// elsewhere — the payment middleware ticker and the broker's
// /v1/cap/{session_id}/end handler respectively.
func (s *Store) RunWatchdog(ctx context.Context, opts LifetimeOptions) {
	interval := opts.CheckInterval
	if interval <= 0 {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.checkOnce(now, opts.IdleTimeout)
		}
	}
}

func (s *Store) checkOnce(now time.Time, idle time.Duration) {
	for _, rec := range s.Snapshot() {
		switch {
		case !rec.Publishing && !rec.ExpiresAt.IsZero() && now.After(rec.ExpiresAt):
			log.Printf("rtmp: session=%s expired without push (no_push_timeout)", rec.SessionID)
			s.terminate(rec, "no_push_timeout")
		case rec.Publishing && idle > 0 && !rec.LastPacketAt.IsZero() && now.Sub(rec.LastPacketAt) > idle:
			log.Printf("rtmp: session=%s idle for %s (idle_timeout)", rec.SessionID, now.Sub(rec.LastPacketAt))
			s.terminate(rec, "idle_timeout")
		}
	}
}

func (s *Store) terminate(rec *SessionRecord, reason string) {
	if rec.Cancel != nil {
		rec.Cancel()
	}
	s.Remove(rec.SessionID)
	log.Printf("rtmp: session=%s terminated reason=%s", rec.SessionID, reason)
}

// Close is the customer-facing CloseSession trigger. Idempotent.
// Returns true when the session existed and was torn down.
func (s *Store) Close(sessionID string, reason string) bool {
	rec := s.Get(sessionID)
	if rec == nil {
		return false
	}
	s.terminate(rec, reason)
	return true
}
