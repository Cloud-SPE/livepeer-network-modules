package main

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
)

// writeV3Keystore generates a V3 JSON keystore file in tmpDir with
// password `pw` using LightScrypt params (the production hardening
// would take ~10s per test). Returns (path, ETH address bytes).
func writeV3Keystore(t *testing.T, tmpDir, pw string) (string, []byte) {
	t.Helper()
	priv, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	id, _ := uuid.NewRandom()
	key := &ethkeystore.Key{
		Id:         id,
		Address:    crypto.PubkeyToAddress(priv.PublicKey),
		PrivateKey: priv,
	}
	enc, err := ethkeystore.EncryptKey(key, pw, ethkeystore.LightScryptN, ethkeystore.LightScryptP)
	if err != nil {
		t.Fatalf("EncryptKey: %v", err)
	}
	path := filepath.Join(tmpDir, "keystore.json")
	if err := os.WriteFile(path, enc, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path, key.Address.Bytes()
}

// captureLogger returns an slog.Logger that writes to the given buffer
// and a handle for asserting against the captured text.
func captureLogger(buf io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// testCfg returns a bootConfig with safe defaults; override fields per
// test. Sender mode by default (it's the simpler boot path — no BoltDB).
func testCfg(t *testing.T) bootConfig {
	t.Helper()
	tmp := t.TempDir()
	return bootConfig{
		mode:       "sender",
		socketPath: filepath.Join(tmp, "daemon.sock"),
		dbPath:     filepath.Join(tmp, "sessions.db"),
	}
}

func TestBuildKeyStoreDevModeUsesDevKeystore(t *testing.T) {
	t.Setenv(passwordEnvVar, "")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := testCfg(t)
	cfg.chainRPC = "" // dev mode

	ks, err := buildKeyStore(logger, cfg)
	if err != nil {
		t.Fatalf("buildKeyStore: %v", err)
	}
	if ks == nil {
		t.Fatal("buildKeyStore returned nil keystore in dev mode")
	}
	addr := ks.Address()
	if len(addr) != 20 {
		t.Errorf("dev keystore address length = %d, want 20", len(addr))
	}
}

func TestBuildKeyStoreProductionRequiresKeystorePath(t *testing.T) {
	t.Setenv(passwordEnvVar, "")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := testCfg(t)
	cfg.chainRPC = "https://example.invalid/rpc"
	cfg.keystorePath = "" // missing

	_, err := buildKeyStore(logger, cfg)
	if err == nil {
		t.Fatal("expected configError, got nil")
	}
	var cfgErr *configError
	if !errors.As(err, &cfgErr) {
		t.Errorf("error not configError: %v", err)
	}
	if !strings.Contains(err.Error(), "--keystore-path is required") {
		t.Errorf("error text: %v", err)
	}
}

func TestBuildKeyStoreProductionDecryptSuccess(t *testing.T) {
	t.Setenv(passwordEnvVar, "hunter2")
	var logBuf bytes.Buffer
	logger := captureLogger(&logBuf)
	cfg := testCfg(t)
	cfg.chainRPC = "https://example.invalid/rpc"
	path, addr := writeV3Keystore(t, t.TempDir(), "hunter2")
	cfg.keystorePath = path

	ks, err := buildKeyStore(logger, cfg)
	if err != nil {
		t.Fatalf("buildKeyStore: %v", err)
	}
	if !bytes.Equal(ks.Address(), addr) {
		t.Errorf("keystore address = %x, want %x", ks.Address(), addr)
	}
	if !strings.Contains(logBuf.String(), "keystore unlocked") {
		t.Errorf("expected unlock log line; got %q", logBuf.String())
	}
}

func TestBuildKeyStoreProductionWrongPassword(t *testing.T) {
	t.Setenv(passwordEnvVar, "wrong-password")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := testCfg(t)
	cfg.chainRPC = "https://example.invalid/rpc"
	path, _ := writeV3Keystore(t, t.TempDir(), "hunter2")
	cfg.keystorePath = path

	_, err := buildKeyStore(logger, cfg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var cfgErr *configError
	if !errors.As(err, &cfgErr) {
		t.Errorf("error not configError: %v", err)
	}
	if !strings.Contains(err.Error(), "decrypt keystore") {
		t.Errorf("expected decrypt-keystore error, got %v", err)
	}
}

func TestBuildKeyStoreProductionMissingFile(t *testing.T) {
	t.Setenv(passwordEnvVar, "hunter2")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := testCfg(t)
	cfg.chainRPC = "https://example.invalid/rpc"
	cfg.keystorePath = filepath.Join(t.TempDir(), "missing.json")

	_, err := buildKeyStore(logger, cfg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "read keystore") {
		t.Errorf("expected read-keystore error, got %v", err)
	}
}

func TestBuildKeyStoreProductionEmptyFile(t *testing.T) {
	t.Setenv(passwordEnvVar, "hunter2")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := testCfg(t)
	cfg.chainRPC = "https://example.invalid/rpc"
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg.keystorePath = path

	_, err := buildKeyStore(logger, cfg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "keystore file is empty") {
		t.Errorf("expected empty-file error, got %v", err)
	}
}

func TestBuildKeyStoreProductionBadJSON(t *testing.T) {
	t.Setenv(passwordEnvVar, "hunter2")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := testCfg(t)
	cfg.chainRPC = "https://example.invalid/rpc"
	dir := t.TempDir()
	path := filepath.Join(dir, "garbage.json")
	if err := os.WriteFile(path, []byte("{not valid keystore"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cfg.keystorePath = path

	_, err := buildKeyStore(logger, cfg)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "decrypt keystore") {
		t.Errorf("expected decrypt error on bad JSON, got %v", err)
	}
}

func TestBuildKeyStoreProductionPasswordViaFile(t *testing.T) {
	t.Setenv(passwordEnvVar, "")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := testCfg(t)
	cfg.chainRPC = "https://example.invalid/rpc"
	dir := t.TempDir()
	path, _ := writeV3Keystore(t, dir, "from-file")
	pwFile := filepath.Join(dir, "pw")
	if err := os.WriteFile(pwFile, []byte("from-file\n"), 0o600); err != nil {
		t.Fatalf("write pw: %v", err)
	}
	cfg.keystorePath = path
	cfg.keystorePwFile = pwFile

	if _, err := buildKeyStore(logger, cfg); err != nil {
		t.Fatalf("buildKeyStore via password file: %v", err)
	}
}

func TestBuildKeyStoreProductionConfigErrorBubblesAsConfigError(t *testing.T) {
	// Verify the *configError wrapper survives all error paths so
	// main()'s os.Exit(configErrExitCode) branch fires.
	t.Setenv(passwordEnvVar, "")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := testCfg(t)
	cfg.chainRPC = "https://example.invalid/rpc"
	cfg.keystorePath = "" // forces "--keystore-path required"

	_, err := buildKeyStore(logger, cfg)
	var cfgErr *configError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("missing config error: %v", err)
	}
}

// ─── Identity-split logging ──────────────────────────────────────────

func TestLogIdentitySplitSingleWalletEmptyOrch(t *testing.T) {
	var buf bytes.Buffer
	logger := captureLogger(&buf)
	signer := bytes.Repeat([]byte{0xab}, 20)
	logIdentitySplit(logger, signer, "")
	out := buf.String()
	if !strings.Contains(out, "single-wallet config") {
		t.Errorf("missing single-wallet WARN: %q", out)
	}
	if !strings.Contains(out, "level=WARN") {
		t.Errorf("expected WARN level: %q", out)
	}
}

func TestLogIdentitySplitSingleWalletExplicitMatch(t *testing.T) {
	var buf bytes.Buffer
	logger := captureLogger(&buf)
	signer := bytes.Repeat([]byte{0xcd}, 20)
	signerHex := "0x" + strings.Repeat("cd", 20)

	logIdentitySplit(logger, signer, signerHex)
	out := buf.String()
	if !strings.Contains(out, "single-wallet config") {
		t.Errorf("missing single-wallet WARN: %q", out)
	}
}

func TestLogIdentitySplitHotColdSplit(t *testing.T) {
	var buf bytes.Buffer
	logger := captureLogger(&buf)
	signer := bytes.Repeat([]byte{0x11}, 20)
	cold := "0x" + strings.Repeat("22", 20)

	logIdentitySplit(logger, signer, cold)
	out := buf.String()
	if !strings.Contains(out, "hot/cold split active") {
		t.Errorf("missing hot/cold INFO: %q", out)
	}
	if strings.Contains(out, "single-wallet") {
		t.Errorf("hot/cold path should not log single-wallet WARN: %q", out)
	}
}

func TestLogIdentitySplitMalformedOrchTreatedAsSingle(t *testing.T) {
	// Locked decision §11.5: a malformed --orch-address shouldn't
	// hard-block startup. We log the WARN so the operator sees it but
	// continue.
	var buf bytes.Buffer
	logger := captureLogger(&buf)
	signer := bytes.Repeat([]byte{0x33}, 20)
	logIdentitySplit(logger, signer, "0xnot-hex")
	out := buf.String()
	if !strings.Contains(out, "single-wallet config") {
		t.Errorf("malformed orch should fire WARN: %q", out)
	}
}

func TestNormalizeAddrHex(t *testing.T) {
	cases := map[string]string{
		"0x" + strings.Repeat("ab", 20):       strings.Repeat("ab", 20),
		strings.Repeat("AB", 20):              strings.Repeat("ab", 20),
		"  0X" + strings.Repeat("01", 20):     strings.Repeat("01", 20),
		"0xtoo-short":                         "",
		"":                                    "",
		"0x" + strings.Repeat("zz", 20):       "",
		"0x" + strings.Repeat("ab", 21):       "",
	}
	for in, want := range cases {
		got := normalizeAddrHex(in)
		if got != want {
			t.Errorf("normalizeAddrHex(%q) = %q, want %q", in, got, want)
		}
	}
}

// The old plan-0017-standalone INFO line was removed when plan 0016
// landed real broker/clock/gas-price providers — the daemon is no
// longer "partially in production mode" with --chain-rpc set.
