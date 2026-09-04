// Command livepeer-payment-daemon serves the Livepeer-Network payment
// surface — sender (`PayerDaemon`) or receiver (`PayeeDaemon`) — over a
// unix socket. Mode is chosen at boot via `--mode`; it does not change
// at runtime.
//
// In production mode (--chain-rpc-urls set), the daemon opens one
// chain-commons multi-RPC client over the whole list — every chain call
// fails over across the entries — resolves the Livepeer Controller
// addresses, and runs against real on-chain state (TicketBroker,
// RoundsManager, BondingManager). The dev-mode path (no --chain-rpc-urls)
// keeps the daemon compileable and testable without any chain
// integration.
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
	"io"
	"log/slog"
	"math/big"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	ethcommon "github.com/ethereum/go-ethereum/common"

	cchain "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	chaincfg "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/config"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/controller"
	ctrleth "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/controller/eth"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/rpc"
	rpcmulti "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/rpc/multi"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/broker/ticketbroker"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/chaincommons"
	clockonchain "github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/clock/onchain"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/devbroker"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/devclock"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/devkeystore"
	gasprice "github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/gasprice/oracle"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/keystore/inmemory"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/keystore/jsonfile"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/server"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/escrow"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/receiver"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/sender"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/service/settlement"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/store"
)

var version = "dev"

const configErrExitCode = 2

func main() {
	var (
		mode          = flag.String("mode", "", "required: 'sender' or 'receiver'")
		socketPath    = flag.String("socket", "", "unix socket the gRPC server listens on (default: per-mode)")
		dbPath        = flag.String("db", "/var/lib/livepeer/payment-daemon/sessions.db", "BoltDB ledger path: receiver sessions, or sender mint-idempotency records")
		maxPaymentWei = flag.String("max-payment-wei", "",
			"sender: REQUIRED in chain mode. Largest funded value this daemon will authorize for a single payment, in wei. "+
				"A circuit breaker against runaway loops and fat-fingered funding, not a price policy — see --max-price-per-unit.")
		maxPricePerUnit = flag.String("max-price-per-unit", "",
			"sender: optional rate ceilings as unit=wei pairs, e.g. 'tokens=10,video_seconds=2000000000000000'. "+
				"Keyed by work unit because that is the denominator prices are quoted in; a unit not listed keeps only the circuit breaker.")
		mintRetention         = flag.Duration("mint-retention", 24*time.Hour, "sender: how long a mint response stays replayable. Keys are remembered forever regardless — an expired key is refused, never re-minted")
		payeeAdminToken       = flag.String("payee-admin-token", "", "Bearer token required for receiver-only PayeeAdmin RPCs. Empty disables authenticated admin access.")
		payerAdminToken       = flag.String("payer-admin-token", "", "Bearer token required for sender-only PayerAdmin RPCs (dev-clock round advancement, for live conformance). Empty disables admin access. Refused outright on a chain clock.")
		chainRPCURLs          = flag.String("chain-rpc-urls", "", "Comma-separated JSON-RPC endpoints, primary first (production). Every chain read and write fails over across the list. Empty = DEV MODE: chain providers are in-memory and the signing key is a deterministic throwaway. The key is REAL secp256k1 — dev payments verify like production ones — but it is published and must never hold value.")
		devKeyHex             = flag.String("dev-signing-key-hex", "", "Dev-mode sender signing key as hex private key (sender only). Rejected when --chain-rpc-urls is set.")
		keystorePath          = flag.String("keystore-path", "", "Path to the V3 JSON keystore file (production only). Required when --chain-rpc-urls is set.")
		keystorePwFile        = flag.String("keystore-password-file", "", "Path to a file containing the keystore unlock password. Mutually exclusive with LIVEPEER_KEYSTORE_PASSWORD.")
		orchAddressHex        = flag.String("orch-address", "", "Hex (0x-prefixed) on-chain orchestrator identity. Empty = the keystore's address is used as the recipient.")
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

		metricsListen = flag.String("metrics-listen", "", "host:port for the Prometheus /metrics HTTP listener; empty (default) disables it.")

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
	rpcURLs, err := chaincfg.ParseRPCURLs(*chainRPCURLs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(configErrExitCode)
	}
	if len(rpcURLs) > 0 && *devKeyHex != "" {
		fmt.Fprintln(os.Stderr, "--dev-signing-key-hex is rejected when --chain-rpc-urls is set")
		os.Exit(configErrExitCode)
	}
	if len(rpcURLs) == 0 {
		fmt.Fprintln(os.Stderr, "livepeer-payment-daemon: DEV MODE — --chain-rpc-urls is empty; using fake chain providers (redemptions will not hit any chain)")
	}

	logger.Info("payment-daemon starting",
		"version", version,
		"mode", *mode,
		"socket", *socketPath,
		"chain", chainStatus(rpcURLs))
	adminToken := *payeeAdminToken
	if adminToken == "" {
		adminToken = os.Getenv("PAYEE_DAEMON_ADMIN_TOKEN")
	}

	cfg := bootConfig{
		mode:                  *mode,
		socketPath:            *socketPath,
		dbPath:                *dbPath,
		mintRetention:         *mintRetention,
		maxPaymentWei:         *maxPaymentWei,
		maxPricePerUnit:       *maxPricePerUnit,
		payeeAdminToken:       adminToken,
		payerAdminToken:       *payerAdminToken,
		chainRPCURLs:          rpcURLs,
		devKeyHex:             *devKeyHex,
		keystorePath:          *keystorePath,
		keystorePwFile:        *keystorePwFile,
		orchAddressHex:        *orchAddressHex,
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
		metricsListen:           *metricsListen,
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
	mode                  string
	socketPath            string
	dbPath                string
	mintRetention         time.Duration
	maxPaymentWei         string
	maxPricePerUnit       string
	payeeAdminToken       string
	payerAdminToken       string
	chainRPCURLs          []string
	devKeyHex             string
	keystorePath          string
	keystorePwFile        string
	orchAddressHex        string
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
	metricsListen           string

	// rpcPolicy overrides chain-commons's default retry / circuit-breaker
	// tuning. nil = chaincfg.Default().RPC. Tests use it to make failover
	// fast; production always runs the default.
	rpcPolicy *chaincfg.RPCPolicy
}

type configError struct{ err error }

func (e *configError) Error() string { return e.err.Error() }
func (e *configError) Unwrap() error { return e.err }

func run(logger *slog.Logger, cfg bootConfig) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runWithContext(ctx, logger, cfg)
}

// runWithContext is run without the signal wiring: the daemon serves
// until ctx is cancelled. Tests drive it directly.
func runWithContext(ctx context.Context, logger *slog.Logger, cfg bootConfig) error {
	if err := ensureParentDir(cfg.socketPath); err != nil {
		return fmt.Errorf("prepare socket dir: %w", err)
	}

	// Build the metrics Recorder. Prometheus when --metrics-listen is set,
	// otherwise a zero-cost Noop. The recorder threads through the gRPC
	// server (interceptor) and the domain services.
	rec := newRecorder(ctx, logger, cfg)

	switch cfg.mode {
	case "sender":
		return runSender(ctx, logger, cfg, rec)
	case "receiver":
		return runReceiver(ctx, logger, cfg, rec)
	default:
		return fmt.Errorf("unknown --mode %q (expected 'sender' or 'receiver')", cfg.mode)
	}
}

// runSender boots a sender-mode daemon. Sender uses the broker
// read-only (GetSenderInfo only) and never submits transactions, so
// TxSigner / GasPrice can stay nil for that path.
func runSender(ctx context.Context, logger *slog.Logger, cfg bootConfig, rec metrics.Recorder) error {
	keystore, err := buildKeyStore(logger, cfg)
	if err != nil {
		return err
	}
	logger.Info("sender identity", "address_hex", fmt.Sprintf("%x", keystore.Address()))
	logIdentitySplit(logger, keystore.Address(), cfg.orchAddressHex)
	var broker providers.Broker
	var clock providers.Clock
	var gp providers.GasPrice

	if len(cfg.chainRPCURLs) == 0 {
		broker = devbroker.New()
		clock = devclock.New()
		gp = providers.NewDevGasPrice()
	} else {
		deps, err := openChain(ctx, logger, rec, cfg)
		if err != nil {
			return err
		}
		defer deps.close()
		_ = gp // sender doesn't need a gas-price provider
		broker, err = ticketbroker.New(ticketbroker.Config{
			Address: deps.addrs.TicketBroker,
			ChainID: big.NewInt(cfg.expectedChainID),
			Logger:  logger,
		}, deps.rpc, nil, nil)
		if err != nil {
			return fmt.Errorf("build broker: %w", err)
		}
		oc, err := clockonchain.New(ctx, clockonchain.Config{
			RoundsManager:   deps.addrs.RoundsManager,
			BondingManager:  deps.addrs.BondingManager,
			RefreshInterval: cfg.clockRefreshInterval,
			Logger:          logger,
		}, deps.rpc)
		if err != nil {
			return fmt.Errorf("build clock: %w", err)
		}
		oc.Start(ctx)
		defer oc.Stop()
		clock = oc
	}
	broker = providers.NewMeteredBroker(broker, rec)

	// Sender mode needs the durable store too: mint idempotency lives
	// there, and without it CreatePayment cannot promise a retry-safe
	// mint — so the daemon refuses to mint rather than risk paying twice.
	if err := ensureParentDir(cfg.dbPath); err != nil {
		return fmt.Errorf("prepare db dir: %w", err)
	}
	st, err := store.Open(cfg.dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	// Age out replay payloads on the operator's window. The permanent
	// tombstones stay: an expired key must refuse, not re-mint, or a
	// delayed retry pays twice.
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n, err := st.EvictMints(time.Now().Add(-cfg.mintRetention)); err == nil && n > 0 {
					logger.Info("evicted mint replay records", "count", n,
						"retention", cfg.mintRetention.String())
				}
			}
		}
	}()

	limits, err := buildLimits(cfg)
	if err != nil {
		return err
	}
	logger.Info("spend limits", "policy", limits.Describe())

	svc := sender.New(
		keystore,
		broker,
		clock,
		logger.With("component", "sender"),
		sender.NewHTTPTicketParamsFetcher(),
		rec,
		st,
		limits,
	)
	// PayerAdmin is mounted only on a dev clock. On a chain clock there
	// is nothing it could legitimately do: rounds are the chain's, and a
	// daemon that could fake them could make an expired payment envelope
	// look live to anything reading its clock.
	var payerAdmin pb.PayerAdminServer
	if dc, ok := clock.(sender.RoundAdvancer); ok {
		payerAdmin = sender.NewAdmin(dc)
		logger.Info("PayerAdmin mounted (dev clock): round advancement available for conformance")
	}
	srv := server.NewSenderWithAdmin(svc, payerAdmin,
		server.SenderAdminConfig{Token: cfg.payerAdminToken},
		cfg.socketPath, rec, logger.With("component", "grpc"))
	return runServerWithCtx(ctx, logger, srv)
}

// runReceiver boots a receiver-mode daemon and lights up the full
// settlement pipeline (broker + escrow + settlement) when
// --chain-rpc-urls is set.
func runReceiver(ctx context.Context, logger *slog.Logger, cfg bootConfig, rec metrics.Recorder) error {
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

	svc := receiver.New(st, receiver.Config{Recipient: recipient, Recorder: rec}, logger.With("component", "receiver"))
	srv := server.NewReceiver(svc, svc, server.ReceiverAdminConfig{Token: cfg.payeeAdminToken}, cfg.socketPath, rec, logger.With("component", "grpc"))

	if len(cfg.chainRPCURLs) > 0 {
		deps, err := openChain(ctx, logger, rec, cfg)
		if err != nil {
			return err
		}
		defer deps.close()

		txSigner, ok := keystore.(providers.TxSigner)
		if !ok {
			return errors.New("keystore does not implement TxSigner; production receiver requires inmemory keystore")
		}

		gp, err := gasprice.New(ctx, gasprice.Config{
			MultiplierPct:   cfg.gasPriceMultPct,
			RefreshInterval: cfg.gasPriceRefreshInterval,
			Logger:          logger,
		}, deps.rpc)
		if err != nil {
			return fmt.Errorf("build gasprice: %w", err)
		}
		gp.Start(ctx)
		defer gp.Stop()

		oc, err := clockonchain.New(ctx, clockonchain.Config{
			RoundsManager:   deps.addrs.RoundsManager,
			BondingManager:  deps.addrs.BondingManager,
			RefreshInterval: cfg.clockRefreshInterval,
			Logger:          logger,
		}, deps.rpc)
		if err != nil {
			return fmt.Errorf("build clock: %w", err)
		}
		oc.Start(ctx)
		defer oc.Stop()

		broker, err := ticketbroker.New(ticketbroker.Config{
			Address:       deps.addrs.TicketBroker,
			Claimant:      ethcommon.BytesToAddress(recipient),
			From:          ethcommon.BytesToAddress(keystore.Address()),
			ChainID:       big.NewInt(cfg.expectedChainID),
			RedeemGas:     cfg.redeemGas,
			Confirmations: cfg.redemptionConfirmations,
			Logger:        logger,
		}, deps.rpc, gp, txSigner)
		if err != nil {
			return fmt.Errorf("build broker: %w", err)
		}
		meteredBroker := providers.NewMeteredBroker(broker, rec)

		// Preflight: fail fast if signing wallet has no ETH for gas.
		bal, err := deps.rpc.BalanceAt(ctx, ethcommon.BytesToAddress(keystore.Address()), nil)
		if err != nil {
			logger.Warn("preflight balance check failed (continuing)", "err", err)
		} else if bal.Sign() == 0 {
			logger.Warn("signing wallet has zero ETH balance — redemptions will fail at gas check until the wallet is funded",
				"address", "0x"+strings.ToLower(fmt.Sprintf("%x", keystore.Address())))
		} else {
			logger.Info("signing wallet ETH balance", "wei", bal.String())
		}

		esc := escrow.New(meteredBroker, oc, escrow.Config{Claimant: recipient, Recorder: rec})
		if err := esc.Rebuild(st); err != nil {
			return fmt.Errorf("escrow rebuild: %w", err)
		}

		set := settlement.New(st, meteredBroker, gp, oc, esc, settlement.Config{
			RedeemGas:      cfg.redeemGas,
			ValidityWindow: cfg.validityWindowRounds,
			Logger:         logger,
			Recorder:       rec,
		})
		go set.Run(ctx, cfg.redemptionInterval)
		defer set.Stop()

		logger.Info("chain integration active",
			"controller", cfg.controllerAddrHex,
			"ticketbroker", deps.addrs.TicketBroker.Hex(),
			"rounds_manager", deps.addrs.RoundsManager.Hex(),
			"bonding_manager", deps.addrs.BondingManager.Hex(),
		)
	}

	return runServerWithCtx(ctx, logger, srv)
}

// chainDeps is what a chain-mode boot shares between providers: the one
// failover RPC client, the Controller-resolved contract addresses, and
// the close that releases both.
type chainDeps struct {
	rpc   rpc.RPC
	addrs controller.Addresses
	close func()
}

// openChain opens the multi-RPC client over --chain-rpc-urls, verifies
// the chain id, and resolves the contract addresses via the Controller
// (honoring per-contract overrides). A dead primary is not fatal: the
// client fails over per call and the circuit breaker parks it.
func openChain(ctx context.Context, logger *slog.Logger, rec metrics.Recorder, cfg bootConfig) (*chainDeps, error) {
	policy := chaincfg.Default().RPC
	if cfg.rpcPolicy != nil {
		policy = *cfg.rpcPolicy
	}
	client, err := rpcmulti.Open(rpcmulti.Options{
		URLs:    cfg.chainRPCURLs,
		Policy:  policy,
		Logger:  chaincommons.Logger(logger.With("component", "rpc")),
		Metrics: chaincommons.Recorder(rec),
	})
	if err != nil {
		return nil, &configError{err: fmt.Errorf("open rpc: %w", err)}
	}
	if err := chain.CheckChainID(ctx, client, cfg.expectedChainID); err != nil {
		_ = client.Close()
		return nil, &configError{err: err}
	}
	logger.Info("chain id verified", "chain_id", cfg.expectedChainID, "endpoints", len(cfg.chainRPCURLs))

	overrides := map[string]cchain.Address{}
	for name, hex := range map[string]string{
		"TicketBroker":   cfg.ticketBrokerAddrHex,
		"RoundsManager":  cfg.roundsManagerAddrHex,
		"BondingManager": cfg.bondingManagerAddrHex,
	} {
		if a := ethcommon.HexToAddress(hex); hex != "" && a != (ethcommon.Address{}) {
			overrides[name] = a
		}
	}
	ctrl, err := ctrleth.New(ctx, ctrleth.Options{
		RPC:               client,
		ControllerAddr:    ethcommon.HexToAddress(cfg.controllerAddrHex),
		ContractOverrides: overrides,
		RefreshInterval:   time.Hour,
		Logger:            chaincommons.Logger(logger.With("component", "controller")),
	})
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("resolve contracts: %w", err)
	}
	closeAll := func() {
		if c, ok := ctrl.(io.Closer); ok {
			_ = c.Close()
		}
		_ = client.Close()
	}
	addrs := ctrl.Addresses()
	for name, a := range map[string]cchain.Address{
		"TicketBroker":   addrs.TicketBroker,
		"RoundsManager":  addrs.RoundsManager,
		"BondingManager": addrs.BondingManager,
	} {
		if a == (cchain.Address{}) {
			closeAll()
			return nil, fmt.Errorf("resolve contracts: resolved %s is zero address", name)
		}
	}
	return &chainDeps{rpc: client, addrs: addrs, close: closeAll}, nil
}

// buildKeyStore returns the providers.KeyStore for the given boot
// config. In dev mode (no chainRPCURLs) it returns a deterministic
// devkeystore. In production mode it loads the V3 JSON keystore via
// jsonfile.Load + inmemory.New (eager decrypt). Decrypt failures are
// wrapped in *configError so the caller exits 2 without binding the
// gRPC socket.
func buildKeyStore(logger *slog.Logger, cfg bootConfig) (providers.KeyStore, error) {
	if len(cfg.chainRPCURLs) == 0 {
		ks, err := devkeystore.New(cfg.devKeyHex)
		if err != nil {
			return nil, &configError{err: fmt.Errorf("dev keystore: %w", err)}
		}
		return ks, nil
	}

	if cfg.keystorePath == "" {
		return nil, &configError{err: errors.New("--keystore-path is required when --chain-rpc-urls is set")}
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

func runServerWithCtx(ctx context.Context, logger *slog.Logger, srv *server.Server) error {
	// Bind before the goroutine, so the socket exists by the time this
	// returns and anything told the daemon is up can actually dial it.
	// It also makes a bind failure — a port in use, a bad path, a
	// permissions problem — a synchronous error rather than one racing
	// ctx.Done() for the return value.
	if err := srv.Listen(); err != nil {
		return err
	}
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

// chainStatus names the endpoints by host only: RPC providers put API
// keys in the path, and this goes to a log line.
func chainStatus(urls []string) string {
	if len(urls) == 0 {
		return "dev (fakes)"
	}
	hosts := make([]string, len(urls))
	for i, u := range urls {
		hosts[i] = chaincommons.Host(u)
	}
	return "production (" + strings.Join(hosts, ",") + ")"
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

// buildLimits assembles the payer's spend policy.
//
// Chain mode REQUIRES --max-payment-wei. A daemon signing against a real
// deposit should have to state, once, the most it will ever authorize in
// one payment; defaulting that to unlimited would make the protection
// opt-in, and the operators most exposed are the least likely to opt in.
// Dev mode has no real funds, so it runs without.
func buildLimits(cfg bootConfig) (sender.Limits, error) {
	var out sender.Limits
	raw := strings.TrimSpace(cfg.maxPaymentWei)
	switch {
	case raw == "" && len(cfg.chainRPCURLs) > 0:
		return out, fmt.Errorf("--max-payment-wei is required in chain mode: " +
			"set the largest funded value this daemon may authorize for one payment " +
			"(see docs/operator-runbook.md, 'Spend limits')")
	case raw != "":
		v, ok := new(big.Int).SetString(raw, 10)
		if !ok || v.Sign() <= 0 {
			return out, fmt.Errorf("--max-payment-wei %q must be a positive decimal integer", raw)
		}
		out.MaxPaymentWei = v
	}
	rates, err := sender.ParseMaxPricePerUnit(cfg.maxPricePerUnit)
	if err != nil {
		return out, fmt.Errorf("--max-price-per-unit: %w", err)
	}
	out.MaxPricePerUnit = rates
	return out, nil
}
