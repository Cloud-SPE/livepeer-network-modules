package ethclient

import (
	"context"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
	ethcommon "github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"

	ccconfig "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/config"
	cctesting "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-payout-executor/internal/config"
)

const testKeyHex = "4c0883a69102937d6231471b5dbb6204fe512961708279ee7c36f6ddf6e702a8"

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
	privateKey, err := crypto.HexToECDSA(testKeyHex)
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

func TestLoadPrivateKeyErrors(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.Executor
		want string
	}{
		{"nothing set", config.Executor{}, "executor.keystore_path + executor.keystore_password_path or executor.private_key_ref is required"},
		{"password only", config.Executor{KeystorePasswordPath: "/x"}, "executor.keystore_path is required"},
		{"keystore only", config.Executor{KeystorePath: "/x"}, "executor.keystore_password_path is required"},
		{"missing keystore file", config.Executor{KeystorePath: "/nonexistent/k", KeystorePasswordPath: "/nonexistent/p"}, "read keystore"},
		{"empty ref", config.Executor{PrivateKeyRef: "env://"}, "secret ref must not be empty"},
		{"unset ref", config.Executor{PrivateKeyRef: "env://POOL_PAYOUT_EXECUTOR_TEST_UNSET_KEY"}, "is not set"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadPrivateKey(tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestLoadPrivateKeyFromRefAndBadHex(t *testing.T) {
	t.Setenv("POOL_PAYOUT_EXECUTOR_TEST_KEY", "0x"+testKeyHex)
	key, err := loadPrivateKey(config.Executor{PrivateKeyRef: "env://POOL_PAYOUT_EXECUTOR_TEST_KEY"})
	if err != nil {
		t.Fatalf("loadPrivateKey(ref) error = %v", err)
	}
	if crypto.PubkeyToAddress(key.PublicKey) == (ethcommon.Address{}) {
		t.Fatal("zero address from ref key")
	}
	t.Setenv("POOL_PAYOUT_EXECUTOR_TEST_KEY", "zz")
	if _, err := loadPrivateKey(config.Executor{PrivateKeyRef: "env://POOL_PAYOUT_EXECUTOR_TEST_KEY"}); err == nil || !strings.Contains(err.Error(), "parse private key") {
		t.Fatalf("bad hex error = %v", err)
	}
	t.Setenv("POOL_PAYOUT_EXECUTOR_TEST_KEY", "")
	if _, err := loadPrivateKey(config.Executor{PrivateKeyRef: "env://POOL_PAYOUT_EXECUTOR_TEST_KEY"}); err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("empty env error = %v", err)
	}
}

func refCfg(t *testing.T, chainID uint64) config.Executor {
	t.Helper()
	t.Setenv("POOL_PAYOUT_EXECUTOR_TEST_KEY", testKeyHex)
	return config.Executor{PrivateKeyRef: "env://POOL_PAYOUT_EXECUTOR_TEST_KEY", ChainID: chainID}
}

func TestNewWithRPC_ChainIDChecks(t *testing.T) {
	fake := cctesting.NewFakeRPC()
	fake.DefaultChainID = 1
	if _, err := NewWithRPC(context.Background(), refCfg(t, 42161), fake); err == nil || !strings.Contains(err.Error(), "does not match configured chain_id 42161") {
		t.Fatalf("mismatch error = %v", err)
	}
	// chain_id 0 disables the check.
	c, err := NewWithRPC(context.Background(), refCfg(t, 0), fake)
	if err != nil {
		t.Fatalf("NewWithRPC(chain_id=0) error = %v", err)
	}
	if c.chainID.Uint64() != 1 {
		t.Fatalf("chainID = %d, want 1 from rpc", c.chainID.Uint64())
	}
	fake.InjectError("ChainID", errors.New("rpc down"))
	if _, err := NewWithRPC(context.Background(), refCfg(t, 42161), fake); err == nil || !strings.Contains(err.Error(), "fetch chain id") {
		t.Fatalf("rpc error = %v", err)
	}
	if _, err := NewWithRPC(context.Background(), refCfg(t, 42161), nil); err == nil {
		t.Fatal("nil rpc accepted")
	}
	if _, err := NewWithRPC(context.Background(), config.Executor{}, fake); err == nil {
		t.Fatal("missing key accepted")
	}
}

func TestNew_RequiresRPCURLs(t *testing.T) {
	_, err := New(context.Background(), config.Executor{RPCURLs: []string{" "}})
	if err == nil || !strings.Contains(err.Error(), "executor.rpc_urls is required") {
		t.Fatalf("New() error = %v", err)
	}
}

func TestNew_OpenFailureWhenNoEndpointDials(t *testing.T) {
	// multi.Open dials eagerly; a URL with an unsupported scheme fails to
	// dial without any network, so Open returns an error and New wraps it.
	_, err := New(context.Background(), config.Executor{RPCURLs: []string{"bogus://nope"}})
	if err == nil || !strings.Contains(err.Error(), "open rpc") {
		t.Fatalf("New() error = %v, want open rpc failure", err)
	}
}

func TestNew_OpensAndVerifiesOverHTTP(t *testing.T) {
	// An http URL dials lazily in go-ethereum, so Open succeeds and the
	// chain-id probe is the first real call. Nothing listens on port 1, so
	// the probe fails through the failover client and New reports it.
	policy := ccconfig.Default().RPC
	policy.MaxRetries = 1
	policy.InitialBackoff = time.Millisecond
	policy.MaxBackoff = time.Millisecond
	policy.CallTimeout = time.Second
	_, err := New(context.Background(), refCfgWithURLs(t, []string{"http://127.0.0.1:1/x"}), Options{Policy: &policy})
	if err == nil || !strings.Contains(err.Error(), "fetch chain id") {
		t.Fatalf("New() error = %v, want fetch chain id failure", err)
	}
}

func refCfgWithURLs(t *testing.T, urls []string) config.Executor {
	cfg := refCfg(t, 42161)
	cfg.RPCURLs = urls
	return cfg
}

func newClient(t *testing.T, fake *cctesting.FakeRPC) *Client {
	t.Helper()
	c, err := NewWithRPC(context.Background(), refCfg(t, 42161), fake)
	if err != nil {
		t.Fatalf("NewWithRPC() error = %v", err)
	}
	return c
}

func TestBalanceAtAndFromAddress(t *testing.T) {
	fake := cctesting.NewFakeRPC()
	fake.DefaultBalance = big.NewInt(12345)
	c := newClient(t, fake)
	bal, err := c.BalanceAt(context.Background())
	if err != nil || bal.Int64() != 12345 {
		t.Fatalf("BalanceAt() = %v, %v", bal, err)
	}
	if c.FromAddress() != c.ks.Address() {
		t.Fatal("FromAddress() mismatch")
	}
	c.Close()
	var nilClient *Client
	nilClient.Close() // must not panic
}

func TestKeystoreAdapter(t *testing.T) {
	fake := cctesting.NewFakeRPC()
	c := newClient(t, fake)
	ks := c.Keystore()
	if ks.Address() != c.FromAddress() {
		t.Fatalf("Keystore().Address() = %s, want %s", ks.Address(), c.FromAddress())
	}
	if c.RPC() != fake {
		t.Fatal("RPC() must return the injected transport")
	}
	if c.ChainID() != 42161 {
		t.Fatalf("ChainID() = %d", c.ChainID())
	}

	tx := ethtypes.NewTx(&ethtypes.DynamicFeeTx{ChainID: big.NewInt(42161), Nonce: 1, Gas: 21000, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2), To: &ethcommon.Address{}, Value: big.NewInt(1)})
	signed, err := ks.SignTx(tx, 42161)
	if err != nil {
		t.Fatalf("SignTx() error = %v", err)
	}
	from, err := ethtypes.Sender(ethtypes.LatestSignerForChainID(big.NewInt(42161)), signed)
	if err != nil || from != c.FromAddress() {
		t.Fatalf("recovered sender = %s, %v; want %s", from, err, c.FromAddress())
	}

	sig, err := ks.Sign([]byte("hello"))
	if err != nil || len(sig) != 65 {
		t.Fatalf("Sign() = %d bytes, %v", len(sig), err)
	}
	prefix := []byte("\x19Ethereum Signed Message:\n5")
	pub, err := crypto.SigToPub(crypto.Keccak256(prefix, []byte("hello")), sig)
	if err != nil || crypto.PubkeyToAddress(*pub) != c.FromAddress() {
		t.Fatalf("Sign() does not recover to the wallet: %v", err)
	}
	raw, err := c.ks.RawSign([]byte("hello"))
	if err != nil || len(raw) != 65 {
		t.Fatalf("RawSign() = %d bytes, %v", len(raw), err)
	}
	pub, err = crypto.SigToPub(crypto.Keccak256([]byte("hello")), raw)
	if err != nil || crypto.PubkeyToAddress(*pub) != c.FromAddress() {
		t.Fatalf("RawSign() does not recover to the wallet: %v", err)
	}
}
