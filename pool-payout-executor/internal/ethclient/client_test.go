package ethclient

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/config"
	ethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestStrip0x(t *testing.T) {
	if got := strip0x("0xabc"); got != "abc" {
		t.Fatalf("strip0x() = %q", got)
	}
}

func TestLoadPrivateKeyFromKeystore(t *testing.T) {
	dir := t.TempDir()
	keyDir := filepath.Join(dir, "keys")
	passwordPath := filepath.Join(dir, "keystore-password")
	ks := ethkeystore.NewKeyStore(keyDir, ethkeystore.LightScryptN, ethkeystore.LightScryptP)
	privateKey, err := crypto.HexToECDSA("4c0883a69102937d6231471b5dbb6204fe512961708279ee7c36f6ddf6e702a8")
	if err != nil {
		t.Fatalf("HexToECDSA() error = %v", err)
	}
	account, err := ks.ImportECDSA(privateKey, "password")
	if err != nil {
		t.Fatalf("ImportECDSA() error = %v", err)
	}
	if err := os.WriteFile(passwordPath, []byte("password\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(password) error = %v", err)
	}
	loaded, err := loadPrivateKey(config.Executor{
		KeystorePath:         account.URL.Path,
		KeystorePasswordPath: passwordPath,
	})
	if err != nil {
		t.Fatalf("loadPrivateKey() error = %v", err)
	}
	if loaded.D.Cmp(privateKey.D) != 0 {
		t.Fatal("loaded private key does not match original key")
	}
}
