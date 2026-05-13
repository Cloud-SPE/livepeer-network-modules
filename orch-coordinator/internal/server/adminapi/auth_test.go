package adminapi

import (
	"errors"
	"testing"
	"time"
)

func TestAuthManager_ExpiresAbsoluteTTL(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	auth := newAuthManager([]string{"token-a"})
	auth.now = func() time.Time { return now }

	sessionID, err := auth.login("token-a", "alice")
	if err != nil {
		t.Fatal(err)
	}

	now = now.Add(sessionAbsoluteTTL)
	if actor, ok := auth.actor(sessionID); ok || actor != "" {
		t.Fatalf("expired session should be rejected; got actor=%q ok=%v", actor, ok)
	}
	if auth.current != nil {
		t.Fatalf("expired session should be cleared")
	}
	if _, err := auth.login("token-a", "bob"); err != nil {
		t.Fatalf("expired slot should be reusable: %v", err)
	}
}

func TestAuthManager_ExpiresIdleTTL(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 13, 12, 0, 0, 0, time.UTC)
	auth := newAuthManager([]string{"token-a"})
	auth.now = func() time.Time { return now }

	sessionID, err := auth.login("token-a", "alice")
	if err != nil {
		t.Fatal(err)
	}

	now = now.Add(sessionIdleTTL - time.Minute)
	if actor, ok := auth.actor(sessionID); !ok || actor != "alice" {
		t.Fatalf("session should still be active; got actor=%q ok=%v", actor, ok)
	}

	now = now.Add(sessionIdleTTL)
	if actor, ok := auth.actor(sessionID); ok || actor != "" {
		t.Fatalf("idle-expired session should be rejected; got actor=%q ok=%v", actor, ok)
	}
	if auth.current != nil {
		t.Fatalf("idle-expired session should be cleared")
	}
}

func TestAuthManager_RejectsConcurrentLiveSession(t *testing.T) {
	t.Parallel()

	auth := newAuthManager([]string{"token-a"})
	if _, err := auth.login("token-a", "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.login("token-a", "bob"); !errors.Is(err, errSessionAlreadyActive) {
		t.Fatalf("second login err = %v; want %v", err, errSessionAlreadyActive)
	}
}
