package policy

import "time"

// RateLimiter enforces max_auto_signs_per_hour with on_breach=pause
// semantics (plan 0042 §7): once breached, *all* auto-signing stops —
// renewals included — until the operator clears it. A burst of
// auto-signs is the loudest available signal that the coordinator
// side is misbehaving, so the limiter latches instead of throttling.
//
// Not safe for concurrent use; the agent loop is single-threaded.
type RateLimiter struct {
	max    int
	window time.Duration
	signs  []time.Time
	paused bool
}

// NewRateLimiter builds a limiter over a sliding 1-hour window.
func NewRateLimiter(maxPerHour int) *RateLimiter {
	return &RateLimiter{max: maxPerHour, window: time.Hour}
}

// Allow reports whether an auto-sign may proceed at now. An attempt
// while the window is already at the cap is the breach: the limiter
// latches into paused and returns false. Allow does not record the
// sign — call RecordSign after the sign actually happens, so a
// failed sign attempt doesn't consume budget.
func (r *RateLimiter) Allow(now time.Time) bool {
	if r.paused {
		return false
	}
	r.prune(now)
	if len(r.signs) >= r.max {
		r.paused = true
		return false
	}
	return true
}

// RecordSign records a completed auto-sign.
func (r *RateLimiter) RecordSign(now time.Time) {
	r.prune(now)
	r.signs = append(r.signs, now)
}

// Paused reports whether the limiter has latched.
func (r *RateLimiter) Paused() bool { return r.paused }

// Clear releases a latched pause and forgets the window — the
// operator gesture after investigating a breach.
func (r *RateLimiter) Clear() {
	r.paused = false
	r.signs = nil
}

func (r *RateLimiter) prune(now time.Time) {
	cutoff := now.Add(-r.window)
	kept := r.signs[:0]
	for _, t := range r.signs {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	r.signs = kept
}
