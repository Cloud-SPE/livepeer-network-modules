// Command livepeer-payment-daemon serves the Livepeer-Network payment
// surface — sender (`PayerDaemon`) or receiver (`PayeeDaemon`) — over a
// unix socket. Mode is chosen at boot via `--mode`; it does not change
// at runtime.
//
// In production mode (--chain-rpc set), the daemon dials an Arbitrum
// One RPC, resolves the Livepeer Controller addresses, and runs against
// real on-chain state (TicketBroker, RoundsManager, BondingManager).
// The dev-mode path (no --chain-rpc) keeps the daemon compileable and
// testable without any chain integration.
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
	"math/big"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	ethcommon "github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"

	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/broker/ticketbroker"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/chain"
	clockonchain "github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/clock/onchain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/devbroker"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/devclock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/devkeystore"
	gasprice "github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/gasprice/onchain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/keystore/inmemory"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/keystore/jsonfile"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/server"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service/escrow"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service/receiver"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service/sender"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service/settlement"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/store"
)

var version = "dev"

const configErrExitCode = 2

func main() {
	var (
		mode                = flag.String("mode", "", "required: 'sender' or 'receiver'")
		socketPath          = flag.String("socket", "", "unix socket the gRPC server listens on (default: per-mode)")
		dbPath              = flag.String("db", "/var/lib/livepeer/payment-daemon/sessions.db", "BoltDB session ledger path (receiver only)")
		chainRPC            = flag.String("chain-rpc", "", "JSON-RPC endpoint (production). Empty = DEV MODE: chain providers and signing key are fakes.")
		devKeyHex           = flag.String("dev-signing-key-hex", "", "Dev-mode sender signing key as hex private key (sender only). Rejected when --chain-rpc is set.")
		keystorePath        = flag.String("keystore-path", "", "Path to the V3 JSON keystore file (production only). Required when --chain-rpc is set.")
		keystorePwFile      = flag.String("keystore-password-file", "", "Path to a file containing the keystore unlock password. Mutually exclusive with LIVEPEER_KEYSTORE_PASSWORD.")
		orchAddressHex      = flag.String("orch-address", "", "Hex (0x-prefixed) on-chain orchestrator identity. Empty = the keystore's address is used as the recipient.")
		controllerAddrHex     = flag.String("chain-controller-address", chain.ArbitrumOneController.Hex(), "Livepeer Controller address. Default = Arbitrum One.")
		ticketBrokerAddrHex   = flag.String("ticketbroker-address", "", "Override TicketBroker address. Empty = resolve via Controller.")
		roundsManagerAddrHex  = flag.String("rounds-manager-address", "", "Override RoundsManager address. Empty = resolve via Controller.")
		bondingManagerAddrHex = flag.String("bonding-manager-address", "", "Override BondingManager address. Empty = resolve via Controller.")
		expectedChainID       = flag.Int64("expected-chain-id", chain.ArbitrumOneChainID, "Expected eth_chainId. 0 = disable check (escape hatch for forks; production must keep the default).")

		gasPriceMultPct            = flag.Uint64("gas-price-multiplier-pct", 200, "Multiplier applied to eth_gasPrice (200 = 2× headroom).")
		redeemGas                  = flag.Uint64("redeem-gas", 500_000, "Gas limit used for redeemWinningTicket (Arbitrum L2 empirical cost).")
		redemptionConfirmations    = flag.Uint64("redemption-confirmations", 4, "Blocks to wait past tx-receipt before declaring confirmed.")
		redemptionIntervalDuration = flag.Duration("redemption-interval", 30*time.Second, "Cadence of the redemption-loop tick (receiver only).")
		validityWindowRounds       = flag.Int64("validity-window", 2, "Drop tickets whose CreationRound is more than this many rounds behind LastInitializedRound.")
		clockRefreshInterval       = flag.Duration("clock-refresh-interval", 30*time.Second, "Cadence of RoundsManager + BondingManager polling.")
		gasPriceRefreshInterval    = flag.Duration("gasprice-refresh-interval", 5*time.Second, "Cadence of eth_gasPrice polling.")

		showVer = flag.Bool("version", false, "print version and exit")
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
		mode:                *mode,
		socketPath:          *socketPath,
		dbPath:              *dbPath,
		chainRPC:            *chainRPC,
		devKeyHex:           *devKeyHex,
		keystorePath:        *keystorePath,
		keystorePwFile:      *keystorePwFile,
		orchAddressHex:      *orchAddressHex,
		controllerAddrHex:     *controllerAddrHex,
		ticketBrokerAddrHex:   *ticketBrokerAddrHex,
		roundsManagerAddrHex:  *roundsManagerAddrHex,
		bondingManagerAddrHex: *bondingManagerAddrHex,
		expectedChainID:       *expectedChainID,

		gasPriceMultPct:         *gasPriceMultPct,
		redeemGas:               *redeemGas,
		redemptionConfirmations: *redemptionConfirmations,
		redemptionInterval:      *redemptionIntervalDuration,
		validityWindowRounds:    *validityWindowRounds,
		clockRefreshInterval:    *clockRefreshInterval,
		gasPriceRefreshInterval: *gasPriceRefreshInterval,
	}
	if err := run(logger, cfg); err != nil {
		var cfgErr *configError
		if errors.As(err, &cfgErr) {
			logger.Error("config error", "err", cfgErr.Unwrap())
			os.Exit(configErrExitCode)
		}
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

type bootConfig struct {
	mode                string
	socketPath          string
	dbPath              string
	chainRPC            string
	devKeyHex           string
	keystorePath        string
	keystorePwFile      string
	orchAddressHex      string
	controllerAddrHex     string
	ticketBrokerAddrHex   string
	roundsManagerAddrHex  string
	bondingManagerAddrHex string
	expectedChainID       int64

	gasPriceMultPct         uint64
	redeemGas               uint64
	redemptionConfirmations uint64
	redemptionInterval      time.Duration
	validityWindowRounds    int64
	clockRefreshInterval    time.Duration
	gasPriceRefreshInterval time.Duration
}

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

// runSender boots a sender-mode daemon. Sender uses the broker
// read-only (GetSenderInfo only) and never submits transactions, so
// TxSigner / GasPrice can stay nil for that path.
func runSender(logger *slog.Logger, cfg bootConfig) error {
	keystore, err := buildKeyStore(logger, cfg)
	if err != nil {
		return err
	}
	logger.Info("sender identity", "address_hex", fmt.Sprintf("%x", keystore.Address()))
	logIdentitySplit(logger, keystore.Address(), cfg.orchAddressHex)
	var broker providers.Broker
	var clock providers.Clock
	var gp providers.GasPrice

	if cfg.chainRPC == "" {
		broker = devbroker.New()
		clock = devclock.New()
		gp = providers.NewDevGasPrice()
	} else {
		ctx := context.Background()
		client, addrs, err := dialAndResolve(ctx, logger, cfg)
		if err != nil {
			return err
		}
		_ = gp // sender doesn't need a gas-price provider
		broker, err = ticketbroker.New(ticketbroker.Config{
			Address: addrs.TicketBroker,
			ChainID: big.NewInt(cfg.expectedChainID),
			Logger:  logger,
		}, client, nil, nil)
		if err != nil {
			return fmt.Errorf("build broker: %w", err)
		}
		oc, err := clockonchain.New(ctx, clockonchain.Config{
			RoundsManager:   addrs.RoundsManager,
			BondingManager:  addrs.BondingManager,
			RefreshInterval: cfg.clockRefreshInterval,
			Logger:          logger,
		}, client)
		if err != nil {
			return fmt.Errorf("build clock: %w", err)
		}
		oc.Start(ctx)
		clock = oc
	}

	svc := sender.New(
		keystore,
		broker,
		clock,
		logger.With("component", "sender"),
		sender.NewHTTPTicketParamsFetcher(),
	)
	srv := server.NewSender(svc, cfg.socketPath, logger.With("component", "grpc"))
	return runServer(logger, srv)
}

// runReceiver boots a receiver-mode daemon and lights up the full
// settlement pipeline (broker + escrow + settlement) when --chain-rpc
// is set.
func runReceiver(logger *slog.Logger, cfg bootConfig) error {
	keystore, err := buildKeyStore(logger, cfg)
	if err != nil {
		return err
	}
	logger.Info("receiver identity", "address_hex", fmt.Sprintf("%x", keystore.Address()))
	logIdentitySplit(logger, keystore.Address(), cfg.orchAddressHex)

	if err := ensureParentDir(cfg.dbPath); err != nil {
		return fmt.Errorf("prepare db dir: %w", err)
	}
	st, err := store.Open(cfg.dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	recipient := keystore.Address()
	if orch := normalizeAddrHex(cfg.orchAddressHex); orch != "" {
		raw, _ := decodeHex40(orch)
		if len(raw) == 20 {
			recipient = raw
		}
	}

	svc := receiver.New(st, receiver.Config{Recipient: recipient}, logger.With("component", "receiver"))
	srv := server.NewReceiver(svc, cfg.socketPath, logger.With("component", "grpc"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cfg.chainRPC != "" {
		client, addrs, err := dialAndResolve(ctx, logger, cfg)
		if err != nil {
			return err
		}

		txSigner, ok := keystore.(providers.TxSigner)
		if !ok {
			return errors.New("keystore does not implement TxSigner; production receiver requires inmemory keystore")
		}

		gp, err := gasprice.New(ctx, gasprice.Config{
			MultiplierPct:   cfg.gasPriceMultPct,
			RefreshInterval: cfg.gasPriceRefreshInterval,
			Logger:          logger,
		}, client)
		if err != nil {
			return fmt.Errorf("build gasprice: %w", err)
		}
		gp.Start(ctx)
		defer gp.Stop()

		oc, err := clockonchain.New(ctx, clockonchain.Config{
			RoundsManager:   addrs.RoundsManager,
			BondingManager:  addrs.BondingManager,
			RefreshInterval: cfg.clockRefreshInterval,
			Logger:          logger,
		}, client)
		if err != nil {
			return fmt.Errorf("build clock: %w", err)
		}
		oc.Start(ctx)
		defer oc.Stop()

		broker, err := ticketbroker.New(ticketbroker.Config{
			Address:       addrs.TicketBroker,
			Claimant:      ethcommon.BytesToAddress(recipient),
			From:          ethcommon.BytesToAddress(keystore.Address()),
			ChainID:       big.NewInt(cfg.expectedChainID),
			RedeemGas:     cfg.redeemGas,
			Confirmations: cfg.redemptionConfirmations,
			Logger:        logger,
		}, client, gp, txSigner)
		if err != nil {
			return fmt.Errorf("build broker: %w", err)
		}

		// Preflight: fail fast if signing wallet has no ETH for gas.
		bal, err := client.BalanceAt(ctx, ethcommon.BytesToAddress(keystore.Address()), nil)
		if err != nil {
			logger.Warn("preflight balance check failed (continuing)", "err", err)
		} else if bal.Sign() == 0 {
			logger.Warn("signing wallet has zero ETH balance — redemptions will fail at gas check until the wallet is funded",
				"address", "0x"+strings.ToLower(fmt.Sprintf("%x", keystore.Address())))
		} else {
			logger.Info("signing wallet ETH balance", "wei", bal.String())
		}

		esc := escrow.New(broker, oc, escrow.Config{Claimant: recipient})
		if err := esc.Rebuild(st); err != nil {
			return fmt.Errorf("escrow rebuild: %w", err)
		}

		set := settlement.New(st, broker, gp, oc, esc, settlement.Config{
			RedeemGas:      cfg.redeemGas,
			ValidityWindow: cfg.validityWindowRounds,
			Logger:         logger,
		})
		go set.Run(ctx, cfg.redemptionInterval)
		defer set.Stop()

		logger.Info("chain integration active",
			"controller", cfg.controllerAddrHex,
			"ticketbroker", addrs.TicketBroker.Hex(),
			"rounds_manager", addrs.RoundsManager.Hex(),
			"bonding_manager", addrs.BondingManager.Hex(),
		)
	}

	return runServerWithCtx(ctx, logger, srv)
}

// dialAndResolve dials the JSON-RPC endpoint, checks the chain ID
// matches `cfg.expectedChainID`, and resolves the contract addresses
// via the Controller (honoring per-contract overrides).
func dialAndResolve(ctx context.Context, logger *slog.Logger, cfg bootConfig) (*ethclient.Client, chain.Addresses, error) {
	client, err := ethclient.DialContext(ctx, cfg.chainRPC)
	if err != nil {
		return nil, chain.Addresses{}, fmt.Errorf("dial %s: %w", cfg.chainRPC, err)
	}
	if err := chain.CheckChainID(ctx, client, cfg.expectedChainID); err != nil {
		client.Close()
		return nil, chain.Addresses{}, &configError{err: err}
	}
	logger.Info("chain id verified", "chain_id", cfg.expectedChainID)

	controllerAddr := ethcommon.HexToAddress(cfg.controllerAddrHex)
	r := chain.NewResolver(client, controllerAddr)
	overrides := chain.Overrides{
		TicketBroker:   ethcommon.HexToAddress(cfg.ticketBrokerAddrHex),
		RoundsManager:  ethcommon.HexToAddress(cfg.roundsManagerAddrHex),
		BondingManager: ethcommon.HexToAddress(cfg.bondingManagerAddrHex),
	}
	addrs, err := r.Resolve(ctx, overrides)
	if err != nil {
		client.Close()
		return nil, chain.Addresses{}, fmt.Errorf("resolve contracts: %w", err)
	}
	return client, addrs, nil
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
	password = "" //nolint:ineffassign,wastedassign // explicit drop
	_ = password
	if err != nil {
		return nil, &configError{err: err}
	}
	if priv == nil {
		return nil, &configError{err: errors.New("decrypt keystore: nil key returned")}
	}

	ks, err := inmemory.New(priv)
	if err != nil {
		return nil, &configError{err: fmt.Errorf("build keystore: %w", err)}
	}
	logger.Info("keystore unlocked", "addr_hex", fmt.Sprintf("%x", ks.Address()))
	priv = (*ecdsa.PrivateKey)(nil) //nolint:ineffassign,wastedassign // explicit drop
	_ = priv
	return ks, nil
}

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

func runServer(logger *slog.Logger, srv *server.Server) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runServerWithCtx(ctx, logger, srv)
}

func runServerWithCtx(ctx context.Context, logger *slog.Logger, srv *server.Server) error {
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
