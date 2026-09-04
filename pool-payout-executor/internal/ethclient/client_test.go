package ethclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
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

func chainIDServer(t *testing.T, hexID string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Method != "eth_chainId" {
			http.Error(w, "unexpected", http.StatusBadRequest)
			return
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":"%s"}`, req.ID, hexID)
	}))
}

func TestDialFirstHealthy_SkipsUnreachableAndWrongChain(t *testing.T) {
	wrong := chainIDServer(t, "0x1")
	defer wrong.Close()
	right := chainIDServer(t, "0xa4b1")
	defer right.Close()
	rpc, chainID, err := dialFirstHealthy(context.Background(), []string{"http://127.0.0.1:1/x", wrong.URL, right.URL}, 42161)
	if err != nil {
		t.Fatalf("dialFirstHealthy() error = %v", err)
	}
	defer rpc.Close()
	if chainID.Uint64() != 42161 {
		t.Fatalf("chain id = %d, want 42161", chainID.Uint64())
	}
}

func TestDialFirstHealthy_NoneUsable(t *testing.T) {
	wrong := chainIDServer(t, "0x1")
	defer wrong.Close()
	_, _, err := dialFirstHealthy(context.Background(), []string{"http://127.0.0.1:1/x", wrong.URL}, 42161)
	if err == nil || !strings.Contains(err.Error(), "2 candidate(s)") {
		t.Fatalf("error = %v, want 'no usable rpc endpoint among 2 candidate(s)'", err)
	}
}

func TestNew_RequiresRPCURLs(t *testing.T) {
	_, err := New(context.Background(), config.Executor{RPCURLs: []string{" "}})
	if err == nil || !strings.Contains(err.Error(), "executor.rpc_urls is required") {
		t.Fatalf("New() error = %v", err)
	}
}

func TestURLHost_DropsSecrets(t *testing.T) {
	if got := urlHost("https://u:p@rpc.example.com/v2/KEY"); got != "rpc.example.com" {
		t.Fatalf("urlHost() = %q", got)
	}
}
