// Command livepeer-payment-daemon serves the Livepeer-Network payment
// surface — sender (`PayerDaemon`) or receiver (`PayeeDaemon`) — over a
// unix socket. Mode is chosen at boot via `--mode`; it does not change
// at runtime.
//
// Plan 0017 lights up V3 JSON keystore loading in production mode (when
// `--chain-rpc` is set). The broker / clock / gas-price providers stay
// dev-stubbed in this dispatch — plan 0016 wires those against the real
// chain.
//
// See ../../docs/operator-runbook.md for what each flag actually does
// and what each failure mode means in production.
package main

import (
	"context"
	"crypto/ecdsa"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/devbroker"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/devclock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/devkeystore"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/keystore/inmemory"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/keystore/jsonfile"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/server"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service/receiver"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service/sender"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/store"
)

var version = "dev"

// configErrExitCode is the exit status the daemon uses for any
// startup configuration failure (bad flags, missing keystore, wrong
// password, …) per plan 0017 §5.2. The exit code is observable; the
// error text is operator-actionable.
const configErrExitCode = 2

func main() {
	var (
		mode               = flag.String("mode", "", "required: 'sender' or 'receiver'")
		socketPath         = flag.String("socket", "", "unix socket the gRPC server listens on (default: per-mode)")
		dbPath             = flag.String("db", "/var/lib/livepeer/payment-daemon/sessions.db", "BoltDB session ledger path (receiver only)")
		chainRPC           = flag.String("chain-rpc", "", "JSON-RPC endpoint (production). Empty = DEV MODE: chain providers and signing key are fakes.")
		devKeyHex          = flag.String("dev-signing-key-hex", "", "Dev-mode sender signing key as hex private key (sender only). Rejected when --chain-rpc is set.")
		keystorePath       = flag.String("keystore-path", "", "Path to the V3 JSON keystore file (production only). Required when --chain-rpc is set.")
		keystorePwFile     = flag.String("keystore-password-file", "", "Path to a file containing the keystore unlock password. Mutually exclusive with LIVEPEER_KEYSTORE_PASSWORD.")
		orchAddressHex     = flag.String("orch-address", "", "Hex (0x-prefixed) on-chain orchestrator identity. Empty = the keystore's address is used as the recipient.")
		showVer            = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println(version)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	if *mode == "" {
		fmt.Fprintln(os.Stderr, "--mode is required (sender|receiver)")
		flag.Usage()
		os.Exit(configErrExitCode)
	}
	if *socketPath == "" {
		switch *mode {
		case "sender":
			*socketPath = "/var/run/livepeer/payer-daemon.sock"
		case "receiver":
			*socketPath = "/var/run/livepeer/payment-daemon.sock"
		}
	}
	if *chainRPC != "" && *devKeyHex != "" {
		fmt.Fprintln(os.Stderr, "--dev-signing-key-hex is rejected when --chain-rpc is set")
		os.Exit(configErrExitCode)
	}
	if *chainRPC == "" {
		fmt.Fprintln(os.Stderr, "livepeer-payment-daemon: DEV MODE — --chain-rpc is empty; using fake chain providers (redemptions will not hit any chain)")
	}

	logger.Info("payment-daemon starting",
		"version", version,
		"mode", *mode,
		"socket", *socketPath,
		"chain", chainStatus(*chainRPC))

	cfg := bootConfig{
		mode:           *mode,
		socketPath:     *socketPath,
		dbPath:         *dbPath,
		chainRPC:       *chainRPC,
		devKeyHex:      *devKeyHex,
		keystorePath:   *keystorePath,
		keystorePwFile: *keystorePwFile,
		orchAddressHex: *orchAddressHex,
	}
	if err := run(logger, cfg); err != nil {
		// Configuration errors exit with code 2 per plan §5.2; runtime
		// failures exit with code 1.
		var cfgErr *configError
		if errors.As(err, &cfgErr) {
			logger.Error("config error", "err", cfgErr.Unwrap())
			os.Exit(configErrExitCode)
		}
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// bootConfig captures the parsed flags + env that govern daemon boot.
// Threaded through run() so subroutines can be exercised in tests
// without invoking the flag parser.
type bootConfig struct {
	mode           string
	socketPath     string
	dbPath         string
	chainRPC       string
	devKeyHex      string
	keystorePath   string
	keystorePwFile string
	orchAddressHex string
}

// configError marks errors that should produce exit code 2 (config
// problem) rather than exit code 1 (runtime fatal). Used by the
// keystore-load path so an unreadable / wrong-password keystore aborts
// before the gRPC socket is bound, per plan §5.2.
type configError struct{ err error }

func (e *configError) Error() string { return e.err.Error() }
func (e *configError) Unwrap() error { return e.err }

func run(logger *slog.Logger, cfg bootConfig) error {
	if err := ensureParentDir(cfg.socketPath); err != nil {
		return fmt.Errorf("prepare socket dir: %w", err)
	}

	switch cfg.mode {
	case "sender":
		return runSender(logger, cfg)
	case "receiver":
		return runReceiver(logger, cfg)
	default:
		return fmt.Errorf("unknown --mode %q (expected 'sender' or 'receiver')", cfg.mode)
	}
}

// runSender boots a sender-mode daemon. Selects between the dev
// keystore (chain-rpc empty) and the V3 keystore (chain-rpc set) per
// plan 0017 §5.4 — eager decrypt before binding the gRPC socket.
func runSender(logger *slog.Logger, cfg bootConfig) error {
	keystore, err := buildKeyStore(logger, cfg)
	if err != nil {
		return err
	}
	logger.Info("sender identity", "address_hex", fmt.Sprintf("%x", keystore.Address()))
	logIdentitySplit(logger, keystore.Address(), cfg.orchAddressHex)
	if cfg.chainRPC != "" {
		logChainRPCStandalone(logger)
	}

	broker := devbroker.New()
	clock := devclock.New()
	_ = providers.NewDevGasPrice() // wired in plan 0015's interim-debit cadence

	svc := sender.New(keystore, broker, clock, logger.With("component", "sender"))
	srv := server.NewSender(svc, cfg.socketPath, logger.With("component", "grpc"))
	return runServer(logger, srv)
}

// runReceiver boots a receiver-mode daemon. Loading + eager-decrypting
// the V3 keystore happens before BoltDB open and before the gRPC
// listener binds, so a bad password fails fast without ever exposing
// the socket.
func runReceiver(logger *slog.Logger, cfg bootConfig) error {
	keystore, err := buildKeyStore(logger, cfg)
	if err != nil {
		return err
	}
	logger.Info("receiver identity", "address_hex", fmt.Sprintf("%x", keystore.Address()))
	logIdentitySplit(logger, keystore.Address(), cfg.orchAddressHex)
	if cfg.chainRPC != "" {
		logChainRPCStandalone(logger)
	}

	if err := ensureParentDir(cfg.dbPath); err != nil {
		return fmt.Errorf("prepare db dir: %w", err)
	}
	st, err := store.Open(cfg.dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	// Recipient defaults to the keystore signer when --orch-address is
	// empty; the hot/cold split log line above already warned the
	// operator if that's not the desired posture.
	recipient := keystore.Address()
	if orch := normalizeAddrHex(cfg.orchAddressHex); orch != "" {
		raw, _ := decodeHex40(orch)
		if len(raw) == 20 {
			recipient = raw
		}
	}
	svc := receiver.New(st, receiver.Config{Recipient: recipient}, logger.With("component", "receiver"))
	srv := server.NewReceiver(svc, cfg.socketPath, logger.With("component", "grpc"))
	return runServer(logger, srv)
}

// buildKeyStore returns the providers.KeyStore for the given boot
// config. In dev mode (cfg.chainRPC == "") it returns a deterministic
// devkeystore. In production mode it loads the V3 JSON keystore via
// jsonfile.Load + inmemory.New (eager decrypt). Decrypt failures are
// wrapped in *configError so the caller exits 2 without binding the
// gRPC socket.
func buildKeyStore(logger *slog.Logger, cfg bootConfig) (providers.KeyStore, error) {
	if cfg.chainRPC == "" {
		ks, err := devkeystore.New(cfg.devKeyHex)
		if err != nil {
			return nil, &configError{err: fmt.Errorf("dev keystore: %w", err)}
		}
		return ks, nil
	}

	if cfg.keystorePath == "" {
		return nil, &configError{err: errors.New("--keystore-path is required when --chain-rpc is set")}
	}

	password, err := loadPassword(cfg.keystorePwFile)
	if err != nil {
		return nil, &configError{err: err}
	}
	priv, err := jsonfile.Load(cfg.keystorePath, password)
	// Drop the password reference; the GC will reclaim the string
	// allocation. Plan §5.5 / §11.6: minimal scrub, no third-party
	// secure-memory dep. The file-read buffer behind the password was
	// already zeroed inside loadPassword.
	password = "" //nolint:ineffassign,wastedassign // explicit drop; see plan §5.5
	_ = password
	if err != nil {
		return nil, &configError{err: err}
	}
	// Defense in depth — make sure jsonfile didn't return nil even on
	// the no-error path. If the loader's contract ever drifts, we'd
	// rather panic at boot than feed a nil key into the signer.
	if priv == nil {
		return nil, &configError{err: errors.New("decrypt keystore: nil key returned")}
	}

	ks, err := inmemory.New(priv)
	if err != nil {
		return nil, &configError{err: fmt.Errorf("build keystore: %w", err)}
	}
	logger.Info("keystore unlocked", "addr_hex", fmt.Sprintf("%x", ks.Address()))
	// Don't keep a reference to the raw key pointer here — inmemory.New
	// holds it, and it never escapes the KeyStore.
	priv = (*ecdsa.PrivateKey)(nil) //nolint:ineffassign,wastedassign // explicit drop
	_ = priv
	return ks, nil
}

// logIdentitySplit emits one of two startup lines per plan 0017 §5.3:
//
//   - WARN single-wallet config: hot signer == on-chain orch identity.
//     Triggered when --orch-address is empty (recipient defaults to the
//     signer) OR --orch-address explicitly equals the keystore address.
//   - INFO hot/cold split active: addresses differ.
//
// The orch-address is parsed best-effort; a malformed value causes the
// WARN to fire so the operator sees something is wrong without
// hard-blocking startup (locked decision §11.5).
func logIdentitySplit(logger *slog.Logger, signer []byte, orchAddressHex string) {
	signerHex := strings.ToLower(fmt.Sprintf("%x", signer))
	orchHex := normalizeAddrHex(orchAddressHex)
	if orchHex == "" || orchHex == signerHex {
		logger.Warn("single-wallet config — hot signer is also the on-chain orchestrator identity. OK for dev, dangerous for prod.",
			"signer", "0x"+signerHex,
			"orch_address", orchHexOrEmpty(orchHex))
		return
	}
	logger.Info("hot/cold split active",
		"signer", "0x"+signerHex,
		"orch_address", "0x"+orchHex)
}

// normalizeAddrHex strips any "0x" prefix and lower-cases. Returns ""
// for an unparseable input so the single-wallet WARN fires (per plan
// §5.3 — we don't hard-block on misconfig; we make it loud).
func normalizeAddrHex(s string) string {
	if s == "" {
		return ""
	}
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	if len(s) != 40 {
		return ""
	}
	for _, c := range s {
		if !isHexDigit(c) {
			return ""
		}
	}
	return strings.ToLower(s)
}

func isHexDigit(c rune) bool {
	switch {
	case c >= '0' && c <= '9':
		return true
	case c >= 'a' && c <= 'f':
		return true
	case c >= 'A' && c <= 'F':
		return true
	}
	return false
}

func orchHexOrEmpty(orchHex string) string {
	if orchHex == "" {
		return "(empty — defaults to signer)"
	}
	return "0x" + orchHex
}

// logChainRPCStandalone is the plan-0017-standalone landing pad: when
// --chain-rpc is set the V3 keystore is active, but plan 0016 has not
// yet wired the real broker/clock/gas-price providers. Operators need
// to know the daemon is *partially* in production mode.
func logChainRPCStandalone(logger *slog.Logger) {
	logger.Info("chain-rpc set: V3 keystore active, but broker/clock/gas-price providers remain dev-mode (plan 0016 wires real chain providers)")
}

func runServer(logger *slog.Logger, srv *server.Server) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve() }()
	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
		srv.GracefulStop()
		if err := <-errCh; err != nil && !errors.Is(err, server.ErrStopped) {
			return err
		}
		return nil
	case err := <-errCh:
		return err
	}
}

func ensureParentDir(p string) error {
	dir := filepath.Dir(p)
	return os.MkdirAll(dir, 0o755)
}

func chainStatus(chainRPC string) string {
	if chainRPC == "" {
		return "dev (fakes)"
	}
	return "production (" + chainRPC + ")"
}

// decodeHex40 decodes a 40-character hex string (no 0x prefix) into 20
// raw bytes. Returns nil on any malformed input — callers fall back to
// their default.
func decodeHex40(hex40 string) ([]byte, error) {
	if len(hex40) != 40 {
		return nil, errors.New("not 40 hex chars")
	}
	out := make([]byte, 20)
	for i := 0; i < 20; i++ {
		hi, err := hexNibble(hex40[2*i])
		if err != nil {
			return nil, err
		}
		lo, err := hexNibble(hex40[2*i+1])
		if err != nil {
			return nil, err
		}
		out[i] = (hi << 4) | lo
	}
	return out, nil
}

func hexNibble(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	}
	return 0, errors.New("non-hex digit")
}
