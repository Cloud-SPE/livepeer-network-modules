// livepeer-capability-broker is the Go reference implementation of the
// workload-agnostic capability broker per the spec at
// livepeer-network-protocol/.
//
// v0.1 status: scaffold only. Flag parsing works; HTTP server, mode drivers,
// extractor library, registry endpoints, and payment client are TODO.
//
// See capability-broker/docs/design-docs/architecture.md for the planned
// package layout and request lifecycle.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

func main() {
	var (
		configPath  = flag.String("config", "/etc/livepeer/host-config.yaml", "path to host-config.yaml")
		listenAddr  = flag.String("listen", ":8080", "HTTP listen address (paid + registry endpoints)")
		metricsAddr = flag.String("metrics", ":9090", "Prometheus metrics listen address")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("livepeer-capability-broker", version)
		return
	}

	log.Printf("livepeer-capability-broker %s\n", version)
	log.Printf("config=%s listen=%s metrics=%s\n", *configPath, *listenAddr, *metricsAddr)
	log.Println("scaffold only — HTTP server, mode drivers, extractors, registry endpoints, and payment client are not yet implemented")
	log.Println("see capability-broker/docs/design-docs/architecture.md for the planned package layout")

	// Exit non-zero so callers don't mistake the scaffold for a working broker.
	os.Exit(2)
}
