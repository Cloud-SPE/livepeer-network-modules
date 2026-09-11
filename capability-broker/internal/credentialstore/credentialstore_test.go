package credentialstore

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T, now *time.Time) *Store {
	t.Helper()
	key := make([]byte, KeySize)
	for i := range key {
		key[i] = byte(i)
	}
	st, err := Open(filepath.Join(t.TempDir(), "creds.db"), key, Options{Now: func() time.Time { return *now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestEnrollAuthenticateRevoke(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	st := openTest(t, &now)
	res, err := st.Enroll(EnrollRequest{HostID: "host-1", Label: "rig"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Token == "" || res.Record.TokenSHA256 != HashToken(res.Token) || res.Record.State != StateActive {
		t.Fatalf("bad enroll result %+v", res)
	}
	rec, err := st.Authenticate(res.Token)
	if err != nil || rec.HostID != "host-1" || !rec.LastUsedAt.Equal(now) {
		t.Fatalf("authenticate: %v %+v", err, rec)
	}
	// Listing never carries secrets.
	list, err := st.List()
	if err != nil || len(list) != 1 || list[0].TokenSHA256 == "" {
		t.Fatalf("list: %v %+v", err, list)
	}
	// Same host again is taken.
	if _, err := st.Enroll(EnrollRequest{HostID: "host-1"}); !errors.Is(err, ErrHostTaken) {
		t.Fatalf("second enroll: %v", err)
	}
	// Revoke: token dead, host free, record kept without hash.
	rev, err := st.Revoke(res.Record.CredentialID, "lost laptop")
	if err != nil || rev.State != StateRevoked || rev.TokenSHA256 != "" || rev.RevokeReason != "lost laptop" {
		t.Fatalf("revoke: %v %+v", err, rev)
	}
	if _, err := st.Authenticate(res.Token); !errors.Is(err, ErrRejected) {
		t.Fatalf("revoked token: %v", err)
	}
	if _, err := st.Enroll(EnrollRequest{HostID: "host-1"}); err != nil {
		t.Fatalf("re-enroll after revoke: %v", err)
	}
	if _, err := st.Rotate(res.Record.CredentialID, time.Hour); !errors.Is(err, ErrRevoked) {
		t.Fatalf("rotate revoked: %v", err)
	}
}

func TestRejectionsAreIndistinguishable(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	st := openTest(t, &now)
	res, _ := st.Enroll(EnrollRequest{HostID: "h", ExpiresIn: time.Hour})
	rev, _ := st.Enroll(EnrollRequest{HostID: "h2"})
	_, _ = st.Revoke(rev.Record.CredentialID, "x")
	now = now.Add(2 * time.Hour) // res expired
	for name, tok := range map[string]string{"unknown": "lpc_nope", "expired": res.Token, "revoked": rev.Token, "empty": ""} {
		rec, err := st.Authenticate(tok)
		if !errors.Is(err, ErrRejected) || rec != nil || err.Error() != ErrRejected.Error() {
			t.Fatalf("%s: err=%v rec=%v", name, err, rec)
		}
	}
	list, _ := st.List()
	for _, r := range list {
		if r.HostID == "h" && r.State != StateExpired {
			t.Fatalf("expired record listed as %s", r.State)
		}
	}
}

func TestRotateGrace(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	st := openTest(t, &now)
	first, _ := st.Enroll(EnrollRequest{HostID: "h"})
	rot, err := st.Rotate(first.Record.CredentialID, 30*time.Minute)
	if err != nil || rot.Token == first.Token || rot.Record.State != StateRotating || rot.Record.Rotation == nil {
		t.Fatalf("rotate: %v %+v", err, rot)
	}
	for _, tok := range []string{first.Token, rot.Token} {
		if _, err := st.Authenticate(tok); err != nil {
			t.Fatalf("during grace %q: %v", tok[:8], err)
		}
	}
	now = now.Add(31 * time.Minute)
	if _, err := st.Authenticate(first.Token); !errors.Is(err, ErrRejected) {
		t.Fatalf("old token after grace: %v", err)
	}
	rec, err := st.Authenticate(rot.Token)
	if err != nil || rec.State != StateActive || rec.Rotation != nil {
		t.Fatalf("new token after grace: %v %+v", err, rec)
	}
}

func TestEnrollReplayReturnsSameToken(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	st := openTest(t, &now)
	a, err := st.Enroll(EnrollRequest{HostID: "h", RequestID: "req-1"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.Enroll(EnrollRequest{HostID: "h", RequestID: "req-1"})
	if err != nil || b.Token != a.Token || b.Record.CredentialID != a.Record.CredentialID {
		t.Fatalf("replay: %v %+v vs %+v", err, a, b)
	}
}

func TestSyncReplace(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	st := openTest(t, &now)
	local, _ := st.Enroll(EnrollRequest{HostID: "local"})
	tokA, tokB := "lpc_a", "lpc_b"
	entries := []SyncEntry{
		{CredentialID: "cred_a", HostID: "host-a", TokenSHA256: HashToken(tokA), ExpiresAt: now.Add(time.Hour)},
		{CredentialID: "cred_b", HostID: "host-b", TokenSHA256: HashToken(tokB), ExpiresAt: now.Add(time.Hour)},
	}
	revoked, err := st.SyncReplace("r1", entries)
	if err != nil || len(revoked) != 0 {
		t.Fatalf("sync1: %v %v", err, revoked)
	}
	for _, tok := range []string{tokA, tokB, local.Token} {
		if _, err := st.Authenticate(tok); err != nil {
			t.Fatalf("after sync %q: %v", tok, err)
		}
	}
	// Idempotent.
	if revoked, err := st.SyncReplace("r1", entries); err != nil || len(revoked) != 0 {
		t.Fatalf("sync repeat: %v %v", err, revoked)
	}
	// Drop b → revoked, a rotated hash, local untouched.
	tokA2 := "lpc_a2"
	revoked, err = st.SyncReplace("r2", []SyncEntry{{CredentialID: "cred_a", HostID: "host-a", TokenSHA256: HashToken(tokA2), ExpiresAt: now.Add(time.Hour)}})
	if err != nil || len(revoked) != 1 || revoked[0] != "host-b" {
		t.Fatalf("sync2: %v %v", err, revoked)
	}
	if _, err := st.Authenticate(tokB); !errors.Is(err, ErrRejected) {
		t.Fatal("dropped entry still authenticates")
	}
	if _, err := st.Authenticate(tokA); !errors.Is(err, ErrRejected) {
		t.Fatal("replaced hash still authenticates")
	}
	if _, err := st.Authenticate(tokA2); err != nil {
		t.Fatalf("new hash: %v", err)
	}
	if _, err := st.Authenticate(local.Token); err != nil {
		t.Fatalf("local after sync: %v", err)
	}
	// Sync cannot take over a local credential.
	if _, err := st.SyncReplace("r3", []SyncEntry{{CredentialID: local.Record.CredentialID, HostID: "local", TokenSHA256: HashToken("x"), ExpiresAt: now.Add(time.Hour)}}); err == nil {
		t.Fatal("sync replaced a locally enrolled credential")
	}
	rev, _ := st.SyncRevision()
	if rev != "r2" {
		t.Fatalf("revision %q", rev)
	}
}

func TestKindGate(t *testing.T) {
	now := time.Now().UTC()
	st := openTest(t, &now)
	if _, err := st.Enroll(EnrollRequest{HostID: "h", Kind: KindEd25519}); !errors.Is(err, ErrKindUnsupported) {
		t.Fatalf("ed25519 enroll: %v", err)
	}
}
