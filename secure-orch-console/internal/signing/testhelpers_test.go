package signing

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
)

// writeV3Keystore encrypts the signer's private key into a V3 JSON
// keystore at <dir>/keystore.json. Returns the file path.
func writeV3Keystore(t *testing.T, dir string, k *Keystore, password string) string {
	t.Helper()
	if k == nil || k.priv == nil {
		t.Fatal("nil keystore")
	}
	id, err := uuid.NewRandom()
	if err != nil {
		t.Fatal(err)
	}
	key := &keystore.Key{
		Id:         id,
		Address:    crypto.PubkeyToAddress(k.priv.PublicKey),
		PrivateKey: k.priv,
	}
	encrypted, err := keystore.EncryptKey(key, password, keystore.LightScryptN, keystore.LightScryptP)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	path := filepath.Join(dir, "keystore.json")
	if err := os.WriteFile(path, encrypted, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}
