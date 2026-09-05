package credentialstore

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// A controller pushes credentials with no expiry — its enrollment model
// has no such field. The credential has to work anyway.
func TestSyncedCredentialWithNoExpiryIsUsable(t *testing.T) {
	st := openTestStore(t)
	token := "attach-secret"
	if _, err := st.SyncReplace("rev-1", []SyncEntry{{
		CredentialID: "host-1", HostID: "host-1", Kind: KindBearer,
		TokenSHA256: HashToken(token),
	}}); err != nil {
		t.Fatalf("SyncReplace() error = %v", err)
	}
	rec, err := st.Authenticate(token)
	if err != nil {
		t.Fatalf("a freshly synced credential was rejected: %v", err)
	}
	if rec.HostID != "host-1" {
		t.Fatalf("HostID = %q", rec.HostID)
	}
	if rec.ExpiresAt.IsZero() {
		t.Fatal("the record kept a zero expiry, which is the expired-on-arrival state")
	}
}

// A pool cannot mint a credential that outlives the store's bound.
func TestSyncedCredentialExpiryIsClampedToMax(t *testing.T) {
	st := openTestStore(t)
	far := time.Now().UTC().Add(10 * 365 * 24 * time.Hour)
	if _, err := st.SyncReplace("rev-1", []SyncEntry{{
		CredentialID: "host-1", HostID: "host-1", Kind: KindBearer,
		TokenSHA256: HashToken("t"), ExpiresAt: far,
	}}); err != nil {
		t.Fatalf("SyncReplace() error = %v", err)
	}
	rec, err := st.Get("host-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !rec.ExpiresAt.Before(far) {
		t.Fatalf("ExpiresAt = %s, which the store's MaxExpiry should have capped", rec.ExpiresAt)
	}
}

// Re-pushing an unchanged credential must not roll its expiry forward,
// or a credential the pool never renews would live forever.
func TestResyncDoesNotExtendAnExistingExpiry(t *testing.T) {
	st := openTestStore(t)
	entry := SyncEntry{CredentialID: "host-1", HostID: "host-1", Kind: KindBearer, TokenSHA256: HashToken("t")}
	if _, err := st.SyncReplace("rev-1", []SyncEntry{entry}); err != nil {
		t.Fatalf("SyncReplace() error = %v", err)
	}
	first, err := st.Get("host-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if _, err := st.SyncReplace("rev-2", []SyncEntry{entry}); err != nil {
		t.Fatalf("SyncReplace() error = %v", err)
	}
	second, err := st.Get("host-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if !second.ExpiresAt.Equal(first.ExpiresAt) {
		t.Fatalf("ExpiresAt moved from %s to %s on a re-push", first.ExpiresAt, second.ExpiresAt)
	}
}

// An expiry the pool set and that has passed is still an expiry.
func TestSyncedCredentialPastItsExpiryIsRejected(t *testing.T) {
	st := openTestStore(t)
	if _, err := st.SyncReplace("rev-1", []SyncEntry{{
		CredentialID: "host-1", HostID: "host-1", Kind: KindBearer,
		TokenSHA256: HashToken("t"), ExpiresAt: time.Now().UTC().Add(-time.Hour),
	}}); err != nil {
		t.Fatalf("SyncReplace() error = %v", err)
	}
	if _, err := st.Authenticate("t"); !errors.Is(err, ErrRejected) {
		t.Fatalf("Authenticate() error = %v, want ErrRejected", err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	st, err := Open(filepath.Join(t.TempDir(), "creds.db"), key, Options{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}
