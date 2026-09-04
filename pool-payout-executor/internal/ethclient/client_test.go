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

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
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
	if c.FromAddress() != crypto.PubkeyToAddress(c.key.PublicKey) {
		t.Fatal("FromAddress() mismatch")
	}
	c.Close()
	var nilClient *Client
	nilClient.Close() // must not panic
}

func TestSendNativeTransfer_HappyPath(t *testing.T) {
	fake := cctesting.NewFakeRPC()
	fake.DefaultNonce = 7
	fake.DefaultGasEstimate = 21000
	fake.HeaderByNumberFunc = func(context.Context, *big.Int) (*ethtypes.Header, error) {
		return &ethtypes.Header{Number: big.NewInt(100), BaseFee: big.NewInt(10)}, nil
	}
	var sent *ethtypes.Transaction
	fake.SendTransactionFunc = func(_ context.Context, tx *ethtypes.Transaction) error { sent = tx; return nil }
	c := newClient(t, fake)
	to := ethcommon.HexToAddress("0x000000000000000000000000000000000000dEaD")
	res, err := c.SendNativeTransfer(context.Background(), to, big.NewInt(1000))
	if err != nil {
		t.Fatalf("SendNativeTransfer() error = %v", err)
	}
	if sent == nil {
		t.Fatal("no transaction reached the rpc")
	}
	if res.Nonce != 7 || sent.Nonce() != 7 {
		t.Fatalf("nonce = %d/%d, want 7", res.Nonce, sent.Nonce())
	}
	if res.TxHash != sent.Hash().Hex() {
		t.Fatalf("tx hash = %s, want %s", res.TxHash, sent.Hash().Hex())
	}
	if sent.Gas() != 21000+2100 {
		t.Fatalf("gas = %d, want estimate + 10%%", sent.Gas())
	}
	// feeCap = 2*baseFee + tip = 20 + 1 gwei
	wantFeeCap := new(big.Int).Add(big.NewInt(20), big.NewInt(1_000_000_000))
	if sent.GasFeeCap().Cmp(wantFeeCap) != 0 {
		t.Fatalf("fee cap = %s, want %s", sent.GasFeeCap(), wantFeeCap)
	}
	if sent.ChainId().Uint64() != 42161 || *sent.To() != to || sent.Value().Int64() != 1000 {
		t.Fatalf("tx fields wrong: chain=%d to=%s value=%s", sent.ChainId().Uint64(), sent.To(), sent.Value())
	}
	signer := ethtypes.LatestSignerForChainID(sent.ChainId())
	from, err := ethtypes.Sender(signer, sent)
	if err != nil || from != c.FromAddress() {
		t.Fatalf("sender = %s, %v; want %s", from, err, c.FromAddress())
	}
}

func TestSendNativeTransfer_NoBaseFeeAndZeroGas(t *testing.T) {
	fake := cctesting.NewFakeRPC()
	fake.DefaultGasEstimate = 0
	c := newClient(t, fake)
	_, err := c.SendNativeTransfer(context.Background(), ethcommon.Address{}, big.NewInt(1))
	if err == nil || !strings.Contains(err.Error(), "estimate gas returned 0") {
		t.Fatalf("zero gas error = %v", err)
	}
}

func TestSendNativeTransfer_ErrorsSurfaceAndDoNotSend(t *testing.T) {
	steps := []string{"PendingNonceAt", "SuggestGasTipCap", "HeaderByNumber", "EstimateGas", "SendTransaction"}
	wants := []string{"pending nonce", "suggest gas tip cap", "latest header", "estimate gas", "send tx"}
	for i, step := range steps {
		t.Run(step, func(t *testing.T) {
			fake := cctesting.NewFakeRPC()
			fake.DefaultGasEstimate = 21000
			fake.InjectError(step, errors.New("boom"))
			sends := 0
			fake.SendTransactionFunc = func(context.Context, *ethtypes.Transaction) error { sends++; return nil }
			c := newClient(t, fake)
			_, err := c.SendNativeTransfer(context.Background(), ethcommon.Address{}, big.NewInt(1))
			if err == nil || !strings.Contains(err.Error(), wants[i]) {
				t.Fatalf("error = %v, want %q", err, wants[i])
			}
			if step != "SendTransaction" && sends != 0 {
				t.Fatalf("a failure at %s still sent %d tx", step, sends)
			}
			// The injected error is consumed; the next attempt succeeds, so
			// a transient failure leaves the client usable.
			if _, err := c.SendNativeTransfer(context.Background(), ethcommon.Address{}, big.NewInt(1)); err != nil {
				t.Fatalf("retry after %s failure: %v", step, err)
			}
		})
	}
}

func receiptRPC(status uint64, block int64) *cctesting.FakeRPC {
	fake := cctesting.NewFakeRPC()
	fake.TransactionReceiptFunc = func(_ context.Context, hash chain.TxHash) (*ethtypes.Receipt, error) {
		return &ethtypes.Receipt{Status: status, BlockNumber: big.NewInt(block), TxHash: hash}, nil
	}
	return fake
}

func TestConfirmTransaction(t *testing.T) {
	const hash = "0x00000000000000000000000000000000000000000000000000000000000000aa"
	t.Run("one confirmation is the receipt itself", func(t *testing.T) {
		c := newClient(t, receiptRPC(ethtypes.ReceiptStatusSuccessful, 50))
		ok, err := c.ConfirmTransaction(context.Background(), hash, 1)
		if err != nil || !ok {
			t.Fatalf("= %v, %v; want true", ok, err)
		}
	})
	t.Run("reverted receipt is not confirmed", func(t *testing.T) {
		c := newClient(t, receiptRPC(ethtypes.ReceiptStatusFailed, 50))
		ok, err := c.ConfirmTransaction(context.Background(), hash, 1)
		if err != nil || ok {
			t.Fatalf("= %v, %v; want false", ok, err)
		}
	})
	t.Run("waits for enough blocks", func(t *testing.T) {
		fake := receiptRPC(ethtypes.ReceiptStatusSuccessful, 50)
		head := int64(51)
		fake.HeaderByNumberFunc = func(context.Context, *big.Int) (*ethtypes.Header, error) {
			return &ethtypes.Header{Number: big.NewInt(head)}, nil
		}
		c := newClient(t, fake)
		ok, err := c.ConfirmTransaction(context.Background(), hash, 3) // needs head+1 >= 53
		if err != nil || ok {
			t.Fatalf("early = %v, %v; want false", ok, err)
		}
		head = 52
		ok, err = c.ConfirmTransaction(context.Background(), hash, 3)
		if err != nil || !ok {
			t.Fatalf("late = %v, %v; want true", ok, err)
		}
	})
	t.Run("missing receipt", func(t *testing.T) {
		fake := cctesting.NewFakeRPC()
		c := newClient(t, fake)
		if _, err := c.ConfirmTransaction(context.Background(), hash, 1); err == nil || !strings.Contains(err.Error(), "transaction receipt") {
			t.Fatalf("error = %v", err)
		}
		fake.TransactionReceiptFunc = func(context.Context, chain.TxHash) (*ethtypes.Receipt, error) { return nil, nil }
		if _, err := c.ConfirmTransaction(context.Background(), hash, 1); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("nil receipt error = %v", err)
		}
	})
	t.Run("header failure", func(t *testing.T) {
		fake := receiptRPC(ethtypes.ReceiptStatusSuccessful, 50)
		fake.InjectError("HeaderByNumber", errors.New("boom"))
		c := newClient(t, fake)
		if _, err := c.ConfirmTransaction(context.Background(), hash, 2); err == nil || !strings.Contains(err.Error(), "latest header") {
			t.Fatalf("error = %v", err)
		}
		fake.HeaderByNumberFunc = func(context.Context, *big.Int) (*ethtypes.Header, error) { return &ethtypes.Header{}, nil }
		if _, err := c.ConfirmTransaction(context.Background(), hash, 2); err == nil || !strings.Contains(err.Error(), "no number") {
			t.Fatalf("no-number error = %v", err)
		}
	})
}
