package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	chaincfg "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/controller"
	ctrleth "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/controller/eth"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/gasoracle"
	gasttl "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/gasoracle/ttl"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/keystore"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/keystore/v3json"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logger"
	cmetrics "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/receipts"
	receiptsreorg "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/receipts/reorg"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc"
	rpcmulti "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc/multi"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/store"
	storebolt "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/store/bolt"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/timesource"
	timesrcpoller "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/timesource/poller"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/roundclock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/txintent"
	chaintesting "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/config"
	aiprovider "github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/providers/aiserviceregistry"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/providers/bondingmanager"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/providers/roundsmanager"
	srprovider "github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/providers/serviceregistry"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/repo/poolhints"
	grpcrt "github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/runtime/grpc"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/runtime/lifecycle"
	aisrservice "github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/aiserviceregistry"
	orchstatussvc "github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/orchstatus"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/preflight"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/reward"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/roundinit"
	srservice "github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/service/serviceregistry"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/types"
)

// run is the testable entry point. Returns:
//
//	0 — clean shutdown
//	1 — runtime / preflight failure
//	2 — flag parse error
func run(ctx context.Context, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("livepeer-protocol-daemon", flag.ContinueOnError)
	fs.SetOutput(stderr)

	mode := fs.String("mode", "both", "round-init | reward | both")
	socketPath := fs.String("socket", "", "unix socket path for the gRPC listener; required in non-dev mode")
	storePath := fs.String("store-path", "", "BoltDB file path; required in non-dev mode")

	ethURLs := fs.String("eth-urls", "", "comma-separated Ethereum RPC URLs (primary first)")
	chainID := fs.Uint64("chain-id", 42161, "expected chain ID; default Arbitrum One")
	controllerAddr := fs.String("controller-address", "0xD8E8328501E9645d16Cf49539efC04f734606ee4", "Livepeer Controller contract address; default Arbitrum One")
	aiServiceRegistryAddr := fs.String("ai-service-registry-address", "0x04C0b249740175999E5BF5c9ac1dA92431EF34C5", "AI service registry contract address; default supplied deployment")
	keystorePath := fs.String("keystore-path", "", "V3 JSON keystore file (required in non-dev mode)")
	keystorePasswordFile := fs.String("keystore-password-file", "", "file containing keystore password; alternative: LIVEPEER_KEYSTORE_PASSWORD env var")
	orchAddress := fs.String("orch-address", "", "orchestrator on-chain address; required in reward / both modes")

	gasLimit := fs.Uint64("gas-limit", 1_000_000, "gas limit for round-init and reward txs")
	minBalanceWei := fs.String("min-balance-wei", "5000000000000000", "preflight: refuse to start when wallet balance is below this (wei, decimal)")
	initJitter := fs.Duration("init-jitter", 0, "max random delay before initializeRound; 0 = disabled")

	metricsListen := fs.String("metrics-listen", "", "host:port for Prometheus listener; empty = disabled")
	metricsMaxSeries := fs.Int("metrics-max-series-per-metric", 10_000, "cap on distinct label tuples per metric; 0 = no cap")

	logLevel := fs.String("log-level", "info", "log level: error|warn|info|debug")
	logFormat := fs.String("log-format", "text", "log format: text|json")

	dev := fs.Bool("dev", false, "use chain-commons.testing fakes (no real chain)")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg := config.Default()
	cfg.Mode = types.Mode(*mode)
	cfg.Dev = *dev
	cfg.Version = version
	cfg.SocketPath = *socketPath
	if cfg.SocketPath == "" && cfg.Dev {
		// Dev / test convenience: fall back to a per-process tmpdir socket
		// so `--dev` is enough to boot. Production must pass --socket.
		cfg.SocketPath = filepath.Join(os.TempDir(), fmt.Sprintf("livepeer-protocol-daemon-%d.sock", os.Getpid()))
	}
	cfg.MetricsListen = *metricsListen
	cfg.MetricsMaxSeries = *metricsMaxSeries
	cfg.InitJitter = *initJitter
	if mb, ok := new(big.Int).SetString(*minBalanceWei, 10); ok {
		cfg.MinBalanceWei = mb
	}
	if *orchAddress != "" {
		cfg.OrchAddress = common.HexToAddress(*orchAddress)
	}
	if *aiServiceRegistryAddr != "" {
		cfg.AIServiceRegistryAddress = common.HexToAddress(*aiServiceRegistryAddr)
	}
	cfg.Chain = chaincfg.Default()
	cfg.Chain.ChainID = chain.ChainID(*chainID)
	cfg.Chain.GasLimit = *gasLimit
	if *ethURLs != "" {
		cfg.Chain.EthURLs = splitCSV(*ethURLs)
	}
	if *controllerAddr != "" {
		cfg.Chain.ControllerAddr = common.HexToAddress(*controllerAddr)
	}
	if *keystorePath != "" {
		cfg.Chain.KeystorePath = *keystorePath
	}
	if *storePath != "" {
		cfg.Chain.StorePath = *storePath
	}

	log, err := buildLogger(*logLevel, *logFormat, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "livepeer-protocol-daemon: %v\n", err)
		return 2
	}
	slog.SetDefault(log)
	clog := newSlogLogger(log)

	if !cfg.Dev {
		// Read keystore password from env or file.
		pw, code := readKeystorePassword(*keystorePasswordFile, stderr)
		if code != 0 {
			return code
		}
		cfg.Chain.KeystorePassword = pw
	}

	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(stderr, "livepeer-protocol-daemon: invalid config: %v\n", err)
		return 2
	}

	if cfg.Dev {
		clog.Warn("DEV MODE — using chain-commons.testing fakes; no chain calls will be made")
	}

	clog.Info("starting protocol-daemon",
		logger.String("mode", cfg.Mode.String()),
		logger.String("version", cfg.Version),
		logger.Uint64("chain_id", uint64(cfg.Chain.ChainID)),
	)

	deps, cleanup, err := buildProviders(ctx, cfg, clog)
	if err != nil {
		clog.Error("provider build failed", logger.Err(err))
		return 1
	}
	defer cleanup()

	// Preflight gate (skipped in dev mode — fakes don't need it; the
	// fakes always return sensible defaults).
	if !cfg.Dev {
		_, err := preflight.Run(ctx, preflight.Config{
			RPC:           deps.RPC,
			Controller:    deps.Controller,
			Keystore:      deps.Keystore,
			GasOracle:     deps.GasOracle,
			ExpectedChain: cfg.Chain.ChainID,
			MinBalanceWei: cfg.MinBalanceWei,
			Mode:          cfg.Mode,
			OrchAddress:   cfg.OrchAddress,
			Logger:        clog,
		})
		if err != nil {
			clog.Error("preflight failed", logger.Err(err))
			return 1
		}
	}

	// TxIntent + Processor wiring. The Manager needs a Processor that
	// signs/broadcasts/tracks; Resume runs once at startup.
	processor, err := txintent.NewDefaultProcessor(txintent.ProcessorConfig{
		Policy:             cfg.Chain.TxIntent,
		ChainID:            cfg.Chain.ChainID,
		ReorgConfirmations: cfg.Chain.ReorgConfirmations,
		GasLimit:           cfg.Chain.GasLimit,
		RPC:                deps.RPC,
		Keystore:           deps.Keystore,
		Gas:                deps.GasOracle,
		Receipts:           deps.Receipts,
		Clock:              deps.Clock,
		Logger:             clog,
		Metrics:            cmetrics.NoOp(),
	})
	if err != nil {
		clog.Error("txintent processor", logger.Err(err))
		return 1
	}
	txm, err := txintent.New(cfg.Chain.TxIntent, deps.Store, deps.Clock, clog, cmetrics.NoOp(), processor)
	if err != nil {
		clog.Error("txintent manager", logger.Err(err))
		return 1
	}
	if err := txm.Resume(ctx); err != nil {
		clog.Error("txintent resume", logger.Err(err))
		return 1
	}

	// Build round-init service if needed.
	var roundInitSvc *roundinit.Service
	if cfg.Mode.HasRoundInit() {
		rmAddr := deps.Controller.Addresses().RoundsManager
		if rmAddr == (chain.Address{}) {
			clog.Error("RoundsManager address not resolved", logger.String("err_code", types.ErrCodePreflightControllerEmpty))
			return 1
		}
		rm, err := roundsmanager.New(deps.RPC, rmAddr)
		if err != nil {
			clog.Error("roundsmanager bindings", logger.Err(err))
			return 1
		}
		roundInitSvc, err = roundinit.New(roundinit.Config{
			RoundsManager: rm,
			TxIntent:      txm,
			Clock:         deps.Clock,
			GasLimit:      cfg.Chain.GasLimit,
			InitJitter:    cfg.InitJitter,
			Logger:        clog,
		})
		if err != nil {
			clog.Error("round-init service", logger.Err(err))
			return 1
		}
	}

	// Build reward service if needed.
	var rewardSvc *reward.Service
	if cfg.Mode.HasReward() {
		bmAddr := deps.Controller.Addresses().BondingManager
		if bmAddr == (chain.Address{}) {
			clog.Error("BondingManager address not resolved", logger.String("err_code", types.ErrCodePreflightControllerEmpty))
			return 1
		}
		bm, err := bondingmanager.New(deps.RPC, bmAddr)
		if err != nil {
			clog.Error("bondingmanager bindings", logger.Err(err))
			return 1
		}
		cache, err := poolhints.New(deps.Store)
		if err != nil {
			clog.Error("pool-hint cache", logger.Err(err))
			return 1
		}
		orch := cfg.OrchAddress
		if orch == (chain.Address{}) {
			// Dev mode default: stamp the keystore-derived address.
			orch = deps.Keystore.Address()
		}
		rewardSvc, err = reward.New(reward.Config{
			BondingManager: bm,
			TxIntent:       txm,
			Cache:          cache,
			Clock:          deps.Clock,
			OrchAddress:    orch,
			GasLimit:       cfg.Chain.GasLimit,
			Logger:         clog,
		})
		if err != nil {
			clog.Error("reward service", logger.Err(err))
			return 1
		}
	}

	var serviceRegistrySvc *srservice.Service
	var orchStatusSvc *orchstatussvc.Service
	var aiServiceRegistrySvc *aisrservice.Service
	var aiOrchStatusSvc *orchstatussvc.Service
	if srAddr := deps.Controller.Addresses().ServiceRegistry; srAddr != (chain.Address{}) {
		reg, err := srprovider.New(srAddr, deps.RPC)
		if err != nil {
			clog.Error("serviceregistry bindings", logger.Err(err))
			return 1
		}
		serviceRegistrySvc, err = srservice.New(srservice.Config{
			Registry: reg,
			TxIntent: txm,
			GasLimit: cfg.Chain.GasLimit,
		})
		if err != nil {
			clog.Error("serviceregistry service", logger.Err(err))
			return 1
		}
		orch := cfg.OrchAddress
		if orch == (chain.Address{}) {
			orch = deps.Keystore.Address()
		}
		orchStatusSvc, err = orchstatussvc.New(orchstatussvc.Config{
			Registry:      reg,
			RPC:           deps.RPC,
			OrchAddress:   orch,
			WalletAddress: deps.Keystore.Address(),
		})
		if err != nil {
			clog.Error("orchstatus service", logger.Err(err))
			return 1
		}
	} else {
		clog.Warn("ServiceRegistry address not resolved; ServiceRegistry status RPCs disabled")
	}

	if cfg.AIServiceRegistryAddress != (chain.Address{}) {
		aiReg, err := aiprovider.New(cfg.AIServiceRegistryAddress, deps.RPC)
		if err != nil {
			clog.Error("ai serviceregistry bindings", logger.Err(err))
			return 1
		}
		aiServiceRegistrySvc, err = aisrservice.New(aisrservice.Config{
			Registry: aiReg,
			TxIntent: txm,
			GasLimit: cfg.Chain.GasLimit,
		})
		if err != nil {
			clog.Error("ai serviceregistry service", logger.Err(err))
			return 1
		}
		orch := cfg.OrchAddress
		if orch == (chain.Address{}) {
			orch = deps.Keystore.Address()
		}
		aiOrchStatusSvc, err = orchstatussvc.New(orchstatussvc.Config{
			Registry:      aiReg,
			RPC:           deps.RPC,
			OrchAddress:   orch,
			WalletAddress: deps.Keystore.Address(),
		})
		if err != nil {
			clog.Error("ai orchstatus service", logger.Err(err))
			return 1
		}
	} else {
		clog.Warn("AI service registry address not configured; AI service registry RPCs disabled")
	}

	// gRPC server + unix-socket listener.
	nativeSrv, err := grpcrt.New(grpcrt.Config{
		Mode:       cfg.Mode,
		Version:    cfg.Version,
		ChainID:    uint64(cfg.Chain.ChainID),
		RoundInit:  roundInitSvc,
		Reward:     rewardSvc,
		Registry:   serviceRegistrySvc,
		Orch:       orchStatusSvc,
		AIRegistry: aiServiceRegistrySvc,
		AIOrch:     aiOrchStatusSvc,
		Tx:         txm,
		RC:         deps.RoundClock,
	})
	if err != nil {
		clog.Error("grpc server build", logger.Err(err))
		return 1
	}
	lis, err := grpcrt.NewListener(grpcrt.ListenerConfig{
		SocketPath: cfg.SocketPath,
		Server:     nativeSrv,
		Logger:     clog,
		Version:    cfg.Version,
	})
	if err != nil {
		clog.Error("grpc listener build", logger.Err(err))
		return 1
	}

	// Lifecycle: run the configured services + listener. Blocks until ctx is cancelled.
	if err := lifecycle.Run(ctx, lifecycle.Config{
		Mode:       cfg.Mode,
		RoundInit:  roundInitSvc,
		Reward:     rewardSvc,
		RoundClock: deps.RoundClock,
		Listener:   lis,
		Logger:     clog,
	}); err != nil {
		clog.Error("lifecycle", logger.Err(err))
		return 1
	}
	return 0
}

// providerSet holds every constructed provider so cleanup can run in
// reverse order at shutdown.
type providerSet struct {
	RPC        rpc.RPC
	Controller controller.Controller
	Keystore   keystore.Keystore
	GasOracle  gasoracle.GasOracle
	Receipts   receipts.Receipts
	TimeSource timesource.TimeSource
	RoundClock roundclock.Clock
	Store      store.Store
	Clock      clock.Clock
}

// buildProviders constructs all chain-commons providers. In dev mode,
// substitutes chain-commons.testing fakes.
func buildProviders(ctx context.Context, cfg config.Config, log logger.Logger) (*providerSet, func(), error) {
	cleanups := []func(){}
	cleanup := func() {
		for i := len(cleanups) - 1; i >= 0; i-- {
			cleanups[i]()
		}
	}

	if cfg.Dev {
		set, err := buildDevProviders(ctx, cfg, log)
		if err != nil {
			return nil, cleanup, err
		}
		cleanups = append(cleanups, func() { _ = set.Store.Close() })
		return set, cleanup, nil
	}

	// --- production providers ---

	rpcClient, err := rpcmulti.Open(rpcmulti.Options{
		URLs:    cfg.Chain.EthURLs,
		Policy:  cfg.Chain.RPC,
		Logger:  log,
		Metrics: cmetrics.NoOp(),
	})
	if err != nil {
		return nil, cleanup, fmt.Errorf("open rpc: %w", err)
	}
	cleanups = append(cleanups, func() { _ = rpcClient.Close() })

	ctrl, err := ctrleth.New(ctx, ctrleth.Options{
		RPC:               rpcClient,
		ControllerAddr:    cfg.Chain.ControllerAddr,
		ContractOverrides: cfg.Chain.ContractOverrides,
		SkipController:    cfg.Chain.SkipController,
		RefreshInterval:   cfg.Chain.ControllerRefreshInterval,
		Logger:            log,
	})
	if err != nil {
		return nil, cleanup, fmt.Errorf("controller resolve: %w", err)
	}

	ks, err := v3json.Open(cfg.Chain.KeystorePath, cfg.Chain.KeystorePassword, cfg.Chain.AccountAddress)
	if err != nil {
		return nil, cleanup, fmt.Errorf("keystore open: %w", err)
	}

	gas, err := gasttl.New(gasttl.Options{
		RPC: rpcClient,
		TTL: cfg.Chain.GasPriceCacheTTL,
		Min: cfg.Chain.GasPriceMin,
		Max: cfg.Chain.GasPriceMax,
	})
	if err != nil {
		return nil, cleanup, fmt.Errorf("gas oracle: %w", err)
	}

	rec, err := receiptsreorg.New(receiptsreorg.Options{
		RPC:  rpcClient,
		Poll: cfg.Chain.BlockPollInterval,
	})
	if err != nil {
		return nil, cleanup, fmt.Errorf("receipts: %w", err)
	}

	ts, err := timesrcpoller.New(timesrcpoller.Options{
		RPC:          rpcClient,
		Controller:   ctrl,
		PollInterval: cfg.Chain.BlockPollInterval,
		Logger:       log,
	})
	if err != nil {
		return nil, cleanup, fmt.Errorf("timesource: %w", err)
	}

	st, err := storebolt.Open(cfg.Chain.StorePath, storebolt.Default())
	if err != nil {
		return nil, cleanup, fmt.Errorf("store open: %w", err)
	}
	cleanups = append(cleanups, func() { _ = st.Close() })

	rclk, err := roundclock.New(roundclock.Options{
		TimeSource: ts,
		Store:      st,
		Logger:     log,
	})
	if err != nil {
		return nil, cleanup, fmt.Errorf("roundclock: %w", err)
	}

	return &providerSet{
		RPC: rpcClient, Controller: ctrl, Keystore: ks, GasOracle: gas,
		Receipts: rec, TimeSource: ts, RoundClock: rclk, Store: st,
		Clock: clock.System(),
	}, cleanup, nil
}

func buildDevProviders(_ context.Context, cfg config.Config, _ logger.Logger) (*providerSet, error) {
	rpcFake := chaintesting.NewFakeRPC()
	rpcFake.DefaultChainID = cfg.Chain.ChainID
	if rpcFake.DefaultChainID == 0 {
		rpcFake.DefaultChainID = 42161
	}
	rpcFake.DefaultBalance = new(big.Int).SetUint64(1e18)

	ks := chaintesting.NewFakeKeystore("dev-mode-protocol-daemon-seed")
	rmAddr := common.HexToAddress("0x000000000000000000000000000000000000FA01")
	bmAddr := common.HexToAddress("0x000000000000000000000000000000000000FB01")

	ctrl := chaintesting.NewFakeController(controller.Addresses{
		RoundsManager:  rmAddr,
		BondingManager: bmAddr,
	}, nil)

	// Fake gas oracle — we don't actually need to do real gas math in dev.
	gas := &devGasOracle{}

	rec := chaintesting.NewFakeReceipts()
	ts := &devTimeSource{}
	rc, err := roundclock.New(roundclock.Options{TimeSource: ts})
	if err != nil {
		return nil, err
	}
	st := store.Memory()
	return &providerSet{
		RPC: rpcFake, Controller: ctrl, Keystore: ks, GasOracle: gas,
		Receipts: rec, TimeSource: ts, RoundClock: rc, Store: st,
		Clock: clock.System(),
	}, nil
}

// devGasOracle returns a constant non-zero estimate so the Suggest call
// doesn't blow up.
type devGasOracle struct{}

func (devGasOracle) Suggest(_ context.Context) (gasoracle.Estimate, error) {
	return gasoracle.Estimate{
		BaseFee: big.NewInt(1_000_000_000),
		TipCap:  big.NewInt(1_000_000_000),
		FeeCap:  big.NewInt(3_000_000_000),
		Source:  "dev",
	}, nil
}
func (devGasOracle) SuggestTipCap(_ context.Context) (chain.Wei, error) {
	return big.NewInt(1_000_000_000), nil
}

// devTimeSource emits no rounds; the dev-mode lifecycle is just smoke
// (boot + idle + shutdown).
type devTimeSource struct{}

func (devTimeSource) CurrentRound(_ context.Context) (chain.Round, error) {
	return chain.Round{Number: 0}, nil
}
func (devTimeSource) CurrentL1Block(_ context.Context) (chain.BlockNumber, error) {
	return 0, nil
}
func (devTimeSource) SubscribeRounds(_ context.Context) (<-chan chain.Round, error) {
	return make(chan chain.Round), nil
}
func (devTimeSource) SubscribeL1Blocks(_ context.Context) (<-chan chain.BlockNumber, error) {
	return make(chan chain.BlockNumber), nil
}

// readKeystorePassword reads the password from --keystore-password-file
// or LIVEPEER_KEYSTORE_PASSWORD env. Returns (pw, exitCode).
func readKeystorePassword(file string, stderr io.Writer) (string, int) {
	if file != "" {
		b, err := os.ReadFile(file) //nolint:gosec
		if err != nil {
			fmt.Fprintf(stderr, "livepeer-protocol-daemon: read keystore password file: %v\n", err)
			return "", 2
		}
		return strings.TrimRight(string(b), "\r\n"), 0
	}
	if pw := os.Getenv("LIVEPEER_KEYSTORE_PASSWORD"); pw != "" {
		return pw, 0
	}
	fmt.Fprintln(stderr, "livepeer-protocol-daemon: keystore password required (set LIVEPEER_KEYSTORE_PASSWORD or --keystore-password-file)")
	return "", 2
}

func buildLogger(level, format string, stderr io.Writer) (*slog.Logger, error) {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		return nil, fmt.Errorf("invalid log level %q", level)
	}
	opts := &slog.HandlerOptions{Level: lvl}
	switch format {
	case "json":
		return slog.New(slog.NewJSONHandler(stderr, opts)), nil
	case "text":
		return slog.New(slog.NewTextHandler(stderr, opts)), nil
	default:
		return nil, fmt.Errorf("invalid log format %q", format)
	}
}

// newSlogLogger wraps an *slog.Logger as chain-commons.providers.logger.Logger
// so it can be passed to chain-commons providers.
func newSlogLogger(l *slog.Logger) logger.Logger {
	return &slogAdapter{l: l}
}

type slogAdapter struct{ l *slog.Logger }

func (s *slogAdapter) toAttrs(fields []logger.Field) []any {
	a := make([]any, 0, len(fields)*2)
	for _, f := range fields {
		a = append(a, f.Key, f.Value)
	}
	return a
}
func (s *slogAdapter) Debug(msg string, fields ...logger.Field) { s.l.Debug(msg, s.toAttrs(fields)...) }
func (s *slogAdapter) Info(msg string, fields ...logger.Field)  { s.l.Info(msg, s.toAttrs(fields)...) }
func (s *slogAdapter) Warn(msg string, fields ...logger.Field)  { s.l.Warn(msg, s.toAttrs(fields)...) }
func (s *slogAdapter) Error(msg string, fields ...logger.Field) { s.l.Error(msg, s.toAttrs(fields)...) }
func (s *slogAdapter) With(fields ...logger.Field) logger.Logger {
	return &slogAdapter{l: s.l.With(s.toAttrs(fields)...)}
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// shutdownTimeout caps how long lifecycle Run waits for services to drain.
// (Currently unused; lifecycle relies on ctx cancellation.) Reserved for
// future use.
var _ = 5 * time.Second

// errExternal is returned when buildProviders fails irrecoverably.
var errExternal = errors.New("provider construction failed")
var _ = errExternal
