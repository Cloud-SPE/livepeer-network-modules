// Package lifecycle ties together provider construction, signal
// handling, and graceful shutdown for the daemon binary.
//
// The daemon binary's main() builds Providers, constructs services,
// constructs a runtime/grpc.Server + Listener, and hands the lot to
// lifecycle.Run. Run blocks until shutdown.
package lifecycle

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/runtime/grpc"
	rmetrics "github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/runtime/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/runtime/seeder"
)

// RunConfig is the input to Run.
type RunConfig struct {
	Server          *grpc.Server
	Listener        *grpc.Listener     // nil → no network listener (used by tests / dry-runs)
	MetricsListener *rmetrics.Listener // nil → no metrics listener
	Seeder          *seeder.Seeder     // nil → no chain auto-discovery (resolver overlay-only / publisher mode)
	Store           store.Store        // closed on shutdown
	Logger          logger.Logger

	// ShutdownTimeout is how long to wait for in-flight work after the
	// first SIGTERM. Defaults to 10s.
	ShutdownTimeout time.Duration
}

// Run blocks until the daemon receives SIGINT or SIGTERM (or ctx is
// cancelled), then shuts down: gRPC graceful-stop → store close.
// Returns nil on clean shutdown.
func Run(ctx context.Context, cfg RunConfig) error {
	if cfg.Server == nil {
		return errors.New("lifecycle: nil Server")
	}
	if cfg.Logger == nil {
		cfg.Logger = logger.Discard()
	}
	if cfg.ShutdownTimeout <= 0 {
		cfg.ShutdownTimeout = 10 * time.Second
	}

	cfg.Logger.Info("daemon started",
		"mode", health(cfg.Server).Mode,
		"cache_size", health(cfg.Server).CacheSize,
	)

	// Wire signals into a child context so both Serve and the wait loop
	// react to the same cancellation.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	sigC := make(chan os.Signal, 2)
	signal.Notify(sigC, syscall.SIGINT, syscall.SIGTERM)

	// Start the gRPC listener if present.
	var serveErr chan error
	if cfg.Listener != nil {
		serveErr = make(chan error, 1)
		go func() { serveErr <- cfg.Listener.Serve(runCtx) }()
	}

	// Start the metrics listener if present. Metrics has its own
	// lifecycle (TCP, not unix-socket); we run it in parallel with
	// the gRPC listener and stop it on the same ctx.
	var metricsErr chan error
	if cfg.MetricsListener != nil {
		metricsErr = make(chan error, 1)
		go func() { metricsErr <- cfg.MetricsListener.Serve(runCtx) }()
	}

	// Start the chain auto-discovery seeder if present (resolver-mode
	// chain-discovery). Errors from inside the seeder loop are
	// logged + swallowed by the seeder itself; only initial
	// subscription failures bubble up here.
	var seederErr chan error
	if cfg.Seeder != nil {
		seederErr = make(chan error, 1)
		go func() { seederErr <- cfg.Seeder.Run(runCtx) }()
	}

	// Wait for signal, ctx cancel, or unexpected listener exit.
	select {
	case <-runCtx.Done():
		cfg.Logger.Info("context cancelled, shutting down")
	case s := <-sigC:
		cfg.Logger.Info("signal received, shutting down", "signal", s.String())
	case err := <-serveErr:
		cfg.Logger.Error("listener exited unexpectedly", "err", err)
		cancel()
		// Allow Listener.Stop() to clean up before falling through.
	case err := <-metricsErr:
		cfg.Logger.Error("metrics listener exited unexpectedly", "err", err)
		cancel()
	case err := <-seederErr:
		// Seeder exited before we asked it to — likely Subscribe
		// failed at startup. Tear down so the operator notices.
		cfg.Logger.Error("seeder exited unexpectedly", "err", err)
		cancel()
	}

	// Trigger listener shutdown. Stop calls are idempotent.
	if cfg.Listener != nil {
		cfg.Listener.Stop()
	}
	if cfg.MetricsListener != nil {
		cfg.MetricsListener.Stop()
	}

	// Wait for serve goroutines to settle (with timeout so a hung
	// handler doesn't block daemon exit).
	drainCtx, drainCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer drainCancel()
	if serveErr != nil {
		select {
		case <-serveErr:
		case <-drainCtx.Done():
			cfg.Logger.Warn("grpc listener did not stop within timeout", "timeout", cfg.ShutdownTimeout.String())
		}
	}
	if metricsErr != nil {
		select {
		case <-metricsErr:
		case <-drainCtx.Done():
			cfg.Logger.Warn("metrics listener did not stop within timeout", "timeout", cfg.ShutdownTimeout.String())
		}
	}
	if seederErr != nil {
		select {
		case <-seederErr:
		case <-drainCtx.Done():
			cfg.Logger.Warn("seeder did not stop within timeout", "timeout", cfg.ShutdownTimeout.String())
		}
	}

	// Close store last.
	if cfg.Store != nil {
		if err := cfg.Store.Close(); err != nil {
			cfg.Logger.Warn("store close error", "err", err)
			return err
		}
	}
	cfg.Logger.Info("daemon stopped")
	return nil
}

// health is a small accessor avoiding an import cycle with grpc tests.
func health(s *grpc.Server) grpc.HealthResult {
	return s.Health(context.Background())
}
