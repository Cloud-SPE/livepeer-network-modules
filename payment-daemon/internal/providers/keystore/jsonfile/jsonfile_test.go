package jsonfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	ethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
)

// writeKeystore generates a V3 JSON keystore file in tmpDir protected
// with `password`. Uses LightScryptN/LightScryptP — production scrypt
// params take ~10s and tests don't need that hardening.
func writeKeystore(t *testing.T, tmpDir, password string) (string, *ethkeystore.Key) {
	t.Helper()
	privKey, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	id, _ := uuid.NewRandom()
	key := &ethkeystore.Key{
		Id:         id,
		Address:    crypto.PubkeyToAddress(privKey.PublicKey),
		PrivateKey: privKey,
	}
	data, err := ethkeystore.EncryptKey(key, password, ethkeystore.LightScryptN, ethkeystore.LightScryptP)
	if err != nil {
		t.Fatalf("encrypt key: %v", err)
	}
	path := filepath.Join(tmpDir, "keystore.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write keystore: %v", err)
	}
	return path, key
}

func TestLoadRoundTrip(t *testing.T) {
	path, key := writeKeystore(t, t.TempDir(), "hunter2")
	got, err := Load(path, "hunter2")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.D.Cmp(key.PrivateKey.D) != 0 {
		t.Errorf("private key D mismatch")
	}
	gotAddr := crypto.PubkeyToAddress(got.PublicKey)
	if gotAddr != key.Address {
		t.Errorf("address = %s, want %s", gotAddr.Hex(), key.Address.Hex())
	}
}

func TestLoadEmptyPath(t *testing.T) {
	_, err := Load("", "pw")
	if err == nil || !strings.Contains(err.Error(), "path is required") {
		t.Fatalf("want path-required error, got %v", err)
	}
}

func TestLoadEmptyPassword(t *testing.T) {
	_, err := Load("/does/not/matter", "")
	if err == nil || !strings.Contains(err.Error(), "password is required") {
		t.Fatalf("want password-required error, got %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "missing.json"), "pw")
	if err == nil || !strings.Contains(err.Error(), "read keystore") {
		t.Fatalf("want read-keystore error, got %v", err)
	}
}

func TestLoadWrongPassword(t *testing.T) {
	path, _ := writeKeystore(t, t.TempDir(), "hunter2")
	_, err := Load(path, "wrong-password")
	if err == nil || !strings.Contains(err.Error(), "decrypt keystore") {
		t.Fatalf("want decrypt error, got %v", err)
	}
}

func TestLoadMalformedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.json")
	if err := os.WriteFile(path, []byte("{not valid keystore json"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Load(path, "pw")
	if err == nil || !strings.Contains(err.Error(), "decrypt keystore") {
		t.Fatalf("want decrypt error on malformed file, got %v", err)
	}
}

func TestLoadEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := Load(path, "pw")
	if err == nil || !strings.Contains(err.Error(), "keystore file is empty") {
		t.Fatalf("want empty-file error, got %v", err)
	}
}
