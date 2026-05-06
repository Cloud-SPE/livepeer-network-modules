// Command livepeer-payment-daemon serves the receiver-side
// PayeeDaemon gRPC service over a unix socket. The capability-broker is
// the only caller in v0.1.
//
// In v0.1 every RPC is a stub: any non-empty Livepeer-Payment ticket is
// accepted, sessions are recorded in BoltDB, and no chain interaction
// happens. See ../DESIGN.md for the boundaries.
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

	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/server"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/store"
)

var version = "dev"

func main() {
	var (
		socketPath = flag.String("socket", "/var/run/livepeer/payment-daemon.sock", "unix socket the gRPC server listens on")
		dbPath     = flag.String("db", "/var/lib/livepeer/payment-daemon/sessions.db", "BoltDB session ledger path")
		showVer    = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println(version)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	logger.Info("payment-daemon starting", "version", version, "socket", *socketPath, "db", *dbPath)

	if err := run(logger, *socketPath, *dbPath); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, socketPath, dbPath string) error {
	if err := ensureParentDir(socketPath); err != nil {
		return fmt.Errorf("prepare socket dir: %w", err)
	}
	if err := ensureParentDir(dbPath); err != nil {
		return fmt.Errorf("prepare db dir: %w", err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	svc := service.New(st, logger.With("component", "service"))
	srv := server.New(svc, socketPath, logger.With("component", "grpc"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve() }()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
		srv.GracefulStop()
		// Drain Serve's return so we surface the real error if any.
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
