package sessionstore

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func testKey() []byte {
	k := make([]byte, KeySize)
	for i := range k {
		k[i] = byte(i)
	}
	return k
}

func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.db")
	s, err := Open(path, testKey())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func sampleRecord() *Record {
	return &Record{
		SessionID:         "sess_1",
		GatewaySessionID:  "gws_1",
		RunnerSessionID:   "rns_1",
		WorkID:            "work_1",
		Capability:        "livepeer:meet/sfu-room",
		Offering:          "default",
		BackendRef:        "backend-a",
		Sender:            []byte{0xAA, 0xBB},
		CredentialHash:    HashSecret("sc_secret"),
		CallbackTokenHash: HashSecret("cb_secret"),
		DescriptorSchema:  "sfu-room/v1",
		DescriptorPublic:  json.RawMessage(`{"url":"wss://sfu","room":"rm_1"}`),
		DescriptorPrivate: json.RawMessage(`{"terminate_token":"rt_topsecret"}`),
		Grants: []GrantAudit{{
			ID:         "grant_1",
			Operations: []string{"participant-token-mint"},
			SecretHash: HashSecret("gs_secret"),
			ExpiresAt:  time.Now().Add(time.Hour).UTC(),
		}},
		Unit:           "participant_minutes",
		LeaseExpiresAt: time.Now().Add(30 * time.Minute).UTC(),
		State:          StateActive,
	}
}

func TestRoundTripAndRestartSurvival(t *testing.T) {
	s, path := openTemp(t)
	rec := sampleRecord()
	rec.LastSequence = 17
	rec.ClaimedTotal = 60
	rec.DebitedTotal = 60
	rec.DebitSeq = 9
	if err := s.Create(rec); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Simulate a broker restart: close and reopen the store.
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	s2, err := Open(path, testKey())
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	got, err := s2.Get("sess_1")
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	if got.DebitSeq != 9 || got.LastSequence != 17 || got.ClaimedTotal != 60 {
		t.Fatalf("counters lost across restart: %+v", got)
	}
	if !bytes.Equal(got.DescriptorPrivate, rec.DescriptorPrivate) {
		t.Fatalf("private part mismatch: %s", got.DescriptorPrivate)
	}
	if !VerifySecret(got.CredentialHash, "sc_secret") {
		t.Fatal("credential no longer verifies after restart")
	}
}

func TestPrivatePartNeverStoredPlaintext(t *testing.T) {
	s, path := openTemp(t)
	if err := s.Create(sampleRecord()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Read the raw bbolt value and assert the secret is absent.
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer db.Close()
	var raw []byte
	_ = db.View(func(tx *bolt.Tx) error {
		raw = bytes.Clone(tx.Bucket([]byte(sessionsBucket)).Get([]byte("sess_1")))
		return nil
	})
	if raw == nil {
		t.Fatal("record missing from raw db")
	}
	if bytes.Contains(raw, []byte("rt_topsecret")) {
		t.Fatal("private descriptor material stored in plaintext")
	}
	if bytes.Contains(raw, []byte("gs_secret")) || bytes.Contains(raw, []byte("sc_secret")) {
		t.Fatal("secret material stored in plaintext")
	}
}

func TestWrongKeyFailsClosed(t *testing.T) {
	s, path := openTemp(t)
	if err := s.Create(sampleRecord()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	wrong := testKey()
	wrong[0] ^= 0xFF
	s2, err := Open(path, wrong)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if _, err := s2.Get("sess_1"); err == nil {
		t.Fatal("expected unseal failure under wrong key, got success")
	}
}

func TestUpdateAtomicCommit(t *testing.T) {
	s, _ := openTemp(t)
	if err := s.Create(sampleRecord()); err != nil {
		t.Fatalf("create: %v", err)
	}
	// The exactly-once shape: a failed commit fn leaves every field
	// untouched, so a retried event re-presents the same sequence.
	boom := errors.New("debit failed")
	err := s.Update("sess_1", func(r *Record) error {
		r.LastEventID = "evt_7"
		r.LastSequence = 1
		r.ClaimedTotal = 5
		r.DebitSeq = 1
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected fn error, got %v", err)
	}
	got, err := s.Get("sess_1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.LastSequence != 0 || got.ClaimedTotal != 0 || got.DebitSeq != 0 || got.LastEventID != "" {
		t.Fatalf("aborted update leaked state: %+v", got)
	}
	// The successful retry commits everything together.
	if err := s.Update("sess_1", func(r *Record) error {
		r.LastEventID = "evt_7"
		r.LastSequence = 1
		r.ClaimedTotal = 5
		r.DebitedTotal = 5
		r.DebitSeq = 1
		return nil
	}); err != nil {
		t.Fatalf("retry update: %v", err)
	}
	got, _ = s.Get("sess_1")
	if got.LastSequence != 1 || got.DebitSeq != 1 || got.DebitedTotal != 5 {
		t.Fatalf("committed update lost state: %+v", got)
	}
}

func TestCreateDuplicateAndGetMissing(t *testing.T) {
	s, _ := openTemp(t)
	if err := s.Create(sampleRecord()); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.Create(sampleRecord()); !errors.Is(err, ErrExists) {
		t.Fatalf("expected ErrExists, got %v", err)
	}
	if _, err := s.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if err := s.Update("nope", func(*Record) error { return nil }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound on update, got %v", err)
	}
}

func TestEvictTerminalBoundsTheStore(t *testing.T) {
	s, _ := openTemp(t)
	now := time.Now().UTC()

	oldEnded := sampleRecord()
	oldEnded.SessionID = "sess_old"
	oldEnded.State = StateEnded
	oldEnded.EndedAt = now.Add(-2 * time.Hour)

	freshEnded := sampleRecord()
	freshEnded.SessionID = "sess_fresh"
	freshEnded.State = StateEnded
	freshEnded.EndedAt = now.Add(-time.Minute)

	active := sampleRecord()
	active.SessionID = "sess_active"

	for _, r := range []*Record{oldEnded, freshEnded, active} {
		if err := s.Create(r); err != nil {
			t.Fatalf("create %s: %v", r.SessionID, err)
		}
	}
	n, err := s.EvictTerminal(now.Add(-time.Hour))
	if err != nil {
		t.Fatalf("evict: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 eviction, got %d", n)
	}
	if _, err := s.Get("sess_old"); !errors.Is(err, ErrNotFound) {
		t.Fatal("old terminal record survived eviction")
	}
	for _, id := range []string{"sess_fresh", "sess_active"} {
		if _, err := s.Get(id); err != nil {
			t.Fatalf("%s wrongly evicted: %v", id, err)
		}
	}
}

func TestVerifySecret(t *testing.T) {
	h := HashSecret("right")
	if !VerifySecret(h, "right") {
		t.Fatal("valid secret rejected")
	}
	if VerifySecret(h, "wrong") {
		t.Fatal("invalid secret accepted")
	}
	if VerifySecret(nil, "right") || VerifySecret([]byte{1, 2}, "right") {
		t.Fatal("malformed hash accepted")
	}
}

func TestForEachSeesUnsealedRecords(t *testing.T) {
	s, _ := openTemp(t)
	a := sampleRecord()
	b := sampleRecord()
	b.SessionID = "sess_2"
	_ = s.Create(a)
	_ = s.Create(b)
	seen := 0
	err := s.ForEach(func(r *Record) error {
		seen++
		if len(r.DescriptorPrivate) == 0 {
			t.Fatalf("record %s iterated with sealed private part", r.SessionID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("foreach: %v", err)
	}
	if seen != 2 {
		t.Fatalf("expected 2 records, saw %d", seen)
	}
}

func TestLoadKeyFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	raw := testKey()
	if got, err := LoadKeyFile(write("raw", raw)); err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("raw key: %v", err)
	}
	hexed := []byte(hex.EncodeToString(raw) + "\n")
	if got, err := LoadKeyFile(write("hex", hexed)); err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("hex key: %v", err)
	}
	if _, err := LoadKeyFile(write("short", []byte("nope"))); err == nil {
		t.Fatal("short key accepted")
	}
	if _, err := LoadKeyFile(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing file accepted")
	}
}
