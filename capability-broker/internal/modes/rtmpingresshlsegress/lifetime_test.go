package rtmpingresshlsegress

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// TestCheckOnce_ExpiresAtTriggersTeardown covers plan §7.1: a session
// that reaches expires_at without an RTMP push is terminated and the
// Cancel func fires.
func TestCheckOnce_ExpiresAtTriggersTeardown(t *testing.T) {
	t.Parallel()

	store := NewStore()
	open := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	expires := open.Add(1 * time.Hour)

	var canceled atomic.Bool
	rec := &SessionRecord{
		SessionID: "sess-no-push",
		ExpiresAt: expires,
		OpenedAt:  open,
		Cancel:    func() { canceled.Store(true) },
	}
	if err := store.Add(rec); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Tick at expires_at + 1s with no publish handshake yet.
	store.checkOnce(expires.Add(time.Second), 10*time.Second)

	if !canceled.Load() {
		t.Fatalf("Cancel was not invoked on no_push_timeout")
	}
	if store.Get("sess-no-push") != nil {
		t.Fatalf("expired session not removed from store")
	}
}

// TestCheckOnce_PublishingSessionIgnoresExpiresAt covers plan §7.1:
// once Publishing is true, the no_push_timeout is no longer relevant.
// expires_at on a publishing session does NOT terminate it.
func TestCheckOnce_PublishingSessionIgnoresExpiresAt(t *testing.T) {
	t.Parallel()

	store := NewStore()
	open := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	expires := open.Add(1 * time.Hour)

	var canceled atomic.Bool
	rec := &SessionRecord{
		SessionID:    "sess-publishing",
		ExpiresAt:    expires,
		OpenedAt:     open,
		Publishing:   true,
		LastPacketAt: expires.Add(-time.Second),
		Cancel:       func() { canceled.Store(true) },
	}
	if err := store.Add(rec); err != nil {
		t.Fatalf("Add: %v", err)
	}

	store.checkOnce(expires.Add(time.Second), 10*time.Second)

	if canceled.Load() {
		t.Fatalf("Cancel fired on a publishing session past expires_at")
	}
	if store.Get("sess-publishing") == nil {
		t.Fatalf("publishing session was removed despite recent packet")
	}
}

// TestCheckOnce_IdleTimeoutTriggersTeardown covers plan §7.2: once
// publishing, no packet for IdleTimeout terminates with idle_timeout.
func TestCheckOnce_IdleTimeoutTriggersTeardown(t *testing.T) {
	t.Parallel()

	store := NewStore()
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

	var canceled atomic.Bool
	rec := &SessionRecord{
		SessionID:    "sess-idle",
		Publishing:   true,
		LastPacketAt: now.Add(-30 * time.Second),
		Cancel:       func() { canceled.Store(true) },
	}
	if err := store.Add(rec); err != nil {
		t.Fatalf("Add: %v", err)
	}

	store.checkOnce(now, 10*time.Second)

	if !canceled.Load() {
		t.Fatalf("Cancel was not invoked on idle_timeout")
	}
	if store.Get("sess-idle") != nil {
		t.Fatalf("idle session not removed from store")
	}
}

// TestCheckOnce_IdleTimeoutZeroDisablesWatchdog confirms a zero
// IdleTimeout disables the idle-timeout branch (used by tests that
// only exercise the no_push_timeout path).
func TestCheckOnce_IdleTimeoutZeroDisablesWatchdog(t *testing.T) {
	t.Parallel()

	store := NewStore()
	now := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)

	var canceled atomic.Bool
	rec := &SessionRecord{
		SessionID:    "sess-no-idle-bound",
		Publishing:   true,
		LastPacketAt: now.Add(-1 * time.Hour),
		Cancel:       func() { canceled.Store(true) },
	}
	if err := store.Add(rec); err != nil {
		t.Fatalf("Add: %v", err)
	}

	store.checkOnce(now, 0)

	if canceled.Load() {
		t.Fatalf("Cancel fired with IdleTimeout=0")
	}
	if store.Get("sess-no-idle-bound") == nil {
		t.Fatalf("session was removed with IdleTimeout=0")
	}
}

// TestStore_Close_TerminatesAndIsIdempotent covers plan §7.4: customer
// CloseSession path. First call fires Cancel + removes the record;
// second call returns false.
func TestStore_Close_TerminatesAndIsIdempotent(t *testing.T) {
	t.Parallel()

	store := NewStore()

	var cancelCount atomic.Int32
	rec := &SessionRecord{
		SessionID: "sess-customer-close",
		Cancel:    func() { cancelCount.Add(1) },
	}
	if err := store.Add(rec); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if !store.Close("sess-customer-close", "customer_close_session") {
		t.Fatalf("first Close returned false")
	}
	if got := cancelCount.Load(); got != 1 {
		t.Fatalf("Cancel invoke count = %d, want 1", got)
	}
	if store.Get("sess-customer-close") != nil {
		t.Fatalf("session not removed after Close")
	}

	// Second close on the same id is a no-op + returns false.
	if store.Close("sess-customer-close", "customer_close_session") {
		t.Fatalf("second Close returned true")
	}
	if got := cancelCount.Load(); got != 1 {
		t.Fatalf("Cancel re-invoked: count = %d", got)
	}
}

// TestStore_Close_UnknownSessionReturnsFalse covers the 404 surface
// from rtmpCloseSession: missing session id is not an error, just
// reports false.
func TestStore_Close_UnknownSessionReturnsFalse(t *testing.T) {
	t.Parallel()

	store := NewStore()
	if store.Close("does-not-exist", "customer_close_session") {
		t.Fatalf("Close on missing session returned true")
	}
}

// TestRunWatchdog_TerminatesOnContextCancel confirms the watchdog
// goroutine returns promptly on context cancellation. The harness uses
// a 50ms CheckInterval so the loop body has a chance to run.
func TestRunWatchdog_TerminatesOnContextCancel(t *testing.T) {
	t.Parallel()

	store := NewStore()
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		store.RunWatchdog(ctx, LifetimeOptions{
			IdleTimeout:   10 * time.Second,
			CheckInterval: 50 * time.Millisecond,
		})
		close(done)
	}()

	// Let the loop tick once.
	time.Sleep(120 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("watchdog did not exit within 2s of context cancel")
	}
}
