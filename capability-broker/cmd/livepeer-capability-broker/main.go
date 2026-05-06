// livepeer-capability-broker is the Go reference implementation of the
// workload-agnostic capability broker per the spec at
// livepeer-network-protocol/.
//
// See capability-broker/docs/design-docs/architecture.md for the planned
// package layout and request lifecycle.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/observability"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/server"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/server/middleware"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

func main() {
	var (
		configPath  = flag.String("config", "/etc/livepeer/host-config.yaml", "path to host-config.yaml")
		listenAddr  = flag.String("listen", "", "HTTP listen address (overrides config)")
		metricsAddr = flag.String("metrics", "", "Prometheus metrics listen address (overrides config)")
		showVersion = flag.Bool("version", false, "print version and exit")

		// Plan 0015 — interim-debit cadence flags.
		interimDebitInterval = flag.Duration(
			"interim-debit-interval",
			30*time.Second,
			"interim-debit tick cadence for long-running sessions; 0 disables the ticker entirely (plan 0015)",
		)
		interimDebitMinRunwayUnits = flag.Uint64(
			"interim-debit-min-runway-units",
			60,
			"minimum required runway in work-units passed to PayeeDaemon.SufficientBalance per tick (plan 0015)",
		)
		interimDebitGraceOnInsufficient = flag.Duration(
			"interim-debit-grace-on-insufficient",
			0,
			"grace period before terminating a handler after SufficientBalance returns false; "+
				"reserved for the future mid-session top-up flow (plan 0015)",
		)
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("livepeer-capability-broker", version)
		return
	}

	observability.SetupLogger()
	log.Printf("livepeer-capability-broker %s", version)

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config load failed: %v", err)
	}
	log.Printf("config loaded from %s; %d capabilities declared", *configPath, len(cfg.Capabilities))

	if *listenAddr != "" {
		cfg.Listen.Paid = *listenAddr
	}
	if *metricsAddr != "" {
		cfg.Listen.Metrics = *metricsAddr
	}

	srv, err := server.New(cfg, server.Options{
		InterimDebit: middleware.InterimDebitConfig{
			Interval:            *interimDebitInterval,
			MinRunwayUnits:      *interimDebitMinRunwayUnits,
			GraceOnInsufficient: *interimDebitGraceOnInsufficient,
		},
	})
	if err != nil {
		log.Fatalf("server init failed: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := srv.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("server error: %v", err)
	}
	log.Println("shutdown complete")
	_ = os.Stdout.Sync()
}
