package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
)

const configUsage = `usage:
  livepeer-capability-broker config validate --config <host-config.yaml>
      Parse and validate a broker config without starting listeners or dialing
      payment-daemon. Intended for deployment preflight.
`

func runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, configUsage)
		return 2
	}
	switch args[0] {
	case "validate":
		fs := flag.NewFlagSet("config validate", flag.ContinueOnError)
		fs.SetOutput(stderr)
		path := fs.String("config", "", "path to host-config.yaml (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *path == "" {
			fmt.Fprintln(stderr, "config validate: --config is required")
			return 2
		}
		cfg, err := config.Load(*path)
		if err != nil {
			fmt.Fprintf(stderr, "config validate: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "valid broker config: offers_source=%s offers=%d\n", cfg.OffersSource, len(cfg.Offers))
		return 0
	default:
		fmt.Fprintf(stderr, "config: unknown subcommand %q\n%s", args[0], configUsage)
		return 2
	}
}
