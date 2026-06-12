package policy

import (
	"testing"
	"time"
)

func TestRateLimiter_LatchesOnBreach(t *testing.T) {
	now := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	rl := NewRateLimiter(2)

	for i := 0; i < 2; i++ {
		if !rl.Allow(now) {
			t.Fatalf("sign %d should be allowed", i+1)
		}
		rl.RecordSign(now)
		now = now.Add(time.Minute)
	}

	// Third attempt inside the hour is the breach: denied AND latched.
	if rl.Allow(now) {
		t.Fatal("breach attempt should be denied")
	}
	if !rl.Paused() {
		t.Fatal("breach must latch the pause")
	}

	// Latched means denied even after the window slides past.
	if rl.Allow(now.Add(2 * time.Hour)) {
		t.Fatal("latched limiter must stay paused until cleared")
	}

	rl.Clear()
	if rl.Paused() {
		t.Fatal("clear must release the latch")
	}
	if !rl.Allow(now.Add(2 * time.Hour)) {
		t.Fatal("cleared limiter with empty window must allow")
	}
}

func TestRateLimiter_WindowSlides(t *testing.T) {
	start := time.Date(2026, 6, 10, 12, 0, 0, 0, time.UTC)
	rl := NewRateLimiter(1)
	if !rl.Allow(start) {
		t.Fatal("first sign should be allowed")
	}
	rl.RecordSign(start)

	// 61 minutes later the window is empty again — no latch ever
	// happened, so the renewal cadence (well under the cap) never
	// trips the limiter.
	later := start.Add(61 * time.Minute)
	if !rl.Allow(later) {
		t.Fatal("sign outside the window should be allowed")
	}
	if rl.Paused() {
		t.Fatal("no breach occurred; limiter must not be paused")
	}
}
