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

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/server"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

func main() {
	var (
		configPath  = flag.String("config", "/etc/livepeer/host-config.yaml", "path to host-config.yaml")
		listenAddr  = flag.String("listen", "", "HTTP listen address (overrides config)")
		metricsAddr = flag.String("metrics", "", "Prometheus metrics listen address (overrides config)")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("livepeer-capability-broker", version)
		return
	}

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

	srv, err := server.New(cfg)
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
