// Command livepeer-payment-daemon serves the Livepeer-Network payment
// surface — sender (`PayerDaemon`) or receiver (`PayeeDaemon`) — over a
// unix socket. Mode is chosen at boot via `--mode`; it does not change
// at runtime.
//
// v0.2 ships with chain providers stubbed (dev-mode broker / clock /
// keystore). Plan 0016 wires real Arbitrum-One integration behind the
// same provider interfaces.
//
// See ../../docs/operator-runbook.md for what each flag actually does
// and what each failure mode means in production.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/devbroker"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/devclock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/providers/devkeystore"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/server"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service/receiver"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service/sender"
)

var version = "dev"

func main() {
	var (
		mode       = flag.String("mode", "", "required: 'sender' or 'receiver'")
		socketPath = flag.String("socket", "", "unix socket the gRPC server listens on (default: per-mode)")
		dbPath     = flag.String("db", "/var/lib/livepeer/payment-daemon/sessions.db", "BoltDB session ledger path (receiver only)")
		chainRPC   = flag.String("chain-rpc", "", "JSON-RPC endpoint (production). Empty = DEV MODE: chain providers are fakes.")
		devKeyHex  = flag.String("dev-signing-key-hex", "", "Dev-mode sender signing key as hex private key (sender only). Rejected when --chain-rpc is set.")
		showVer    = flag.Bool("version", false, "print version and exit")
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
		os.Exit(2)
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
		os.Exit(2)
	}
	if *chainRPC == "" {
		fmt.Fprintln(os.Stderr, "livepeer-payment-daemon: DEV MODE — --chain-rpc is empty; using fake chain providers (redemptions will not hit any chain)")
	}

	logger.Info("payment-daemon starting",
		"version", version,
		"mode", *mode,
		"socket", *socketPath,
		"chain", chainStatus(*chainRPC))

	if err := run(logger, *mode, *socketPath, *dbPath, *chainRPC, *devKeyHex); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, mode, socketPath, dbPath, chainRPC, devKeyHex string) error {
	if err := ensureParentDir(socketPath); err != nil {
		return fmt.Errorf("prepare socket dir: %w", err)
	}

	switch mode {
	case "sender":
		return runSender(logger, socketPath, chainRPC, devKeyHex)
	case "receiver":
		return runReceiver(logger, socketPath, dbPath, chainRPC)
	default:
		return fmt.Errorf("unknown --mode %q (expected 'sender' or 'receiver')", mode)
	}
}

func runSender(logger *slog.Logger, socketPath, chainRPC, devKeyHex string) error {
	if chainRPC != "" {
		return errors.New("chain integration is plan 0016; --chain-rpc is not yet supported")
	}
	keystore, err := devkeystore.New(devKeyHex)
	if err != nil {
		return fmt.Errorf("dev keystore: %w", err)
	}
	logger.Info("sender identity", "address_hex", fmt.Sprintf("%x", keystore.Address()))

	broker := devbroker.New()
	clock := devclock.New()
	_ = providers.NewDevGasPrice() // wired in plan 0015's interim-debit cadence

	svc := sender.New(keystore, broker, clock, logger.With("component", "sender"))
	srv := server.NewSender(svc, socketPath, logger.With("component", "grpc"))
	return runServer(logger, srv)
}

func runReceiver(logger *slog.Logger, socketPath, dbPath, chainRPC string) error {
	if chainRPC != "" {
		return errors.New("chain integration is plan 0016; --chain-rpc is not yet supported")
	}
	if err := ensureParentDir(dbPath); err != nil {
		return fmt.Errorf("prepare db dir: %w", err)
	}
	// v0.2 receiver scaffold returns Unimplemented; plan 0014 C4 wires
	// the real surface. Intentionally NOT opening the BoltDB store yet.
	svc := receiver.New(logger.With("component", "receiver"))
	srv := server.NewReceiver(svc, socketPath, logger.With("component", "grpc"))
	return runServer(logger, srv)
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
