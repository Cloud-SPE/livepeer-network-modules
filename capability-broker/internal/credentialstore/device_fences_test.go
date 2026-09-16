package credentialstore

import (
	"path/filepath"
	"testing"
	"time"
)

func TestDeviceRevocationSurvivesRestartAndDelayedFullSync(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.db")
	key := make([]byte, 32)
	st, err := Open(path, key, Options{})
	if err != nil {
		t.Fatal(err)
	}
	entry := SyncEntry{CredentialID: "credential", HostID: "host", PoolID: "pool", TokenSHA256: HashToken("secret"), ExpiresAt: time.Now().Add(time.Hour), DeviceOwnership: map[string]uint64{"gpu-a": 1, "gpu-b": 1}}
	if _, err := st.SyncReplace("initial", []SyncEntry{entry}); err != nil {
		t.Fatal(err)
	}
	if err := st.RevokeDevice("pool", "host", "GPU-A", 2, "transfer"); err == nil {
		t.Fatal("wrong generation revoked")
	}
	if err := st.RevokeDevice("pool", "host", "GPU-A", 1, "transfer"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path, key, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.SyncReplace("delayed", []SyncEntry{entry}); err != nil {
		t.Fatal(err)
	}
	record, err := st.ByHost("host")
	if err != nil {
		t.Fatal(err)
	}
	if record.DeviceOwnership["gpu-a"] != 0 || record.DeviceOwnership["gpu-b"] != 1 {
		t.Fatal("stale full sync revived device or removed sibling", record.DeviceOwnership)
	}
	if err := st.RevokeDevice("pool", "host", "gpu-a", 1, "retry"); err != nil {
		t.Fatal(err)
	}
	entry.DeviceOwnership["gpu-a"] = 2
	if _, err := st.SyncReplace("later-assignment", []SyncEntry{entry}); err != nil {
		t.Fatal(err)
	}
	record, err = st.ByHost("host")
	if err != nil || record.DeviceOwnership["gpu-a"] != 2 {
		t.Fatalf("future generation blocked %+v %v", record, err)
	}
	revoked, err := st.DeviceRevoked("pool", "host", "gpu-a", 1)
	if err != nil || !revoked {
		t.Fatal("old proof disappeared", err)
	}
	revoked, err = st.DeviceRevoked("pool", "host", "gpu-a", 2)
	if err != nil || revoked {
		t.Fatal("new assignment incorrectly revoked", err)
	}
}

func TestCredentialGenerationRejectsDelayedSecretAndRevival(t *testing.T) {
	now := time.Now().UTC()
	st := openTest(t, &now)
	entry := SyncEntry{CredentialID: "host", HostID: "host", PoolID: "pool", TokenSHA256: HashToken("old"), CredentialGeneration: 1, ExpiresAt: now.Add(time.Hour)}
	if _, err := st.SyncReplace("initial", []SyncEntry{entry}); err != nil {
		t.Fatal(err)
	}
	rotated := entry
	rotated.CredentialGeneration = 2
	rotated.TokenSHA256 = HashToken("new")
	if _, err := st.SyncReplace("rotated", []SyncEntry{rotated}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SyncReplace("late", []SyncEntry{entry}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Authenticate("old"); err == nil {
		t.Fatal("delayed sync restored old secret")
	}
	if _, err := st.Authenticate("new"); err != nil {
		t.Fatal(err)
	}
	conflict := rotated
	conflict.TokenSHA256 = HashToken("other")
	if _, err := st.SyncReplace("conflict", []SyncEntry{conflict}); err == nil {
		t.Fatal("same generation changed secret")
	}
	if _, err := st.SyncReplace("revoked", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SyncReplace("late-active", []SyncEntry{rotated}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Authenticate("new"); err == nil {
		t.Fatal("delayed active snapshot revived revoked credential")
	}
}
