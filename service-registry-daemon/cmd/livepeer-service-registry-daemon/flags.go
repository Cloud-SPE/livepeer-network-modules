package main

import (
	"flag"
	"os"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/config"
)

// parseFlags processes argv into a *config.Daemon. Returns (cfg, nil)
// on success, (nil, err) on parse / validate failure. The bool return
// reports whether the user asked for `--help` (caller exits 0).
func parseFlags(args []string) (*config.Daemon, bool, error) {
	cfg := config.DefaultDaemon()

	fs := flag.NewFlagSet("livepeer-service-registry-daemon", flag.ContinueOnError)
	fs.SetOutput(stderr())

	mode := fs.String("mode", "", "operating mode: publisher | resolver (required)")
	fs.StringVar(&cfg.SocketPath, "socket", cfg.SocketPath, "unix socket path for gRPC")
	fs.StringVar(&cfg.StorePath, "store-path", cfg.StorePath, "BoltDB file path")
	fs.StringVar(&cfg.ChainRPC, "chain-rpc", cfg.ChainRPC, "Ethereum JSON-RPC endpoint (mutually exclusive with --dev)")
	fs.Int64Var(&cfg.ChainID, "chain-id", cfg.ChainID, "expected chain ID (sanity check)")
	fs.StringVar(&cfg.ControllerAddress, "controller-address", cfg.ControllerAddress, "Livepeer Controller contract address; used for resolver chain auto-discovery (BondingManager + RoundsManager). Default Arbitrum One")
	fs.StringVar(&cfg.ServiceRegistryAddress, "service-registry-address", cfg.ServiceRegistryAddress, "optional override for the primary registry contract address; when empty, resolver reads ServiceRegistry from Controller")
	fs.StringVar(&cfg.AIServiceRegistryAddress, "ai-service-registry-address", cfg.AIServiceRegistryAddress, "AI registry contract address; when set, resolver lookups use this registry instead of the primary/controller-derived registry")
	fs.StringVar(&cfg.LogFormat, "log-format", cfg.LogFormat, "text|json")
	fs.StringVar(&cfg.LogLevel, "log-level", cfg.LogLevel, "debug|info|warn|error")
	fs.BoolVar(&cfg.Dev, "dev", cfg.Dev, "use in-memory fakes; throwaway key in publisher mode")

	// Resolver-only
	discovery := fs.String("discovery", string(cfg.Discovery), `cache seeding source: "chain" (walk BondingManager pool on each round event) or "overlay-only" (--static-overlay only)`)
	fs.DurationVar(&cfg.RoundPollInterval, "round-poll-interval", cfg.RoundPollInterval, "how often the chain-commons timesource polls RoundsManager.currentRound() to detect round transitions (resolver chain-discovery mode)")
	fs.DurationVar(&cfg.CacheManifestTTL, "cache-manifest-ttl", cfg.CacheManifestTTL, "TTL for fetched manifest cache")
	fs.Int64Var(&cfg.ManifestMaxBytes, "manifest-max-bytes", cfg.ManifestMaxBytes, "hard cap on manifest body size")
	fs.DurationVar(&cfg.ManifestFetchTimeout, "manifest-fetch-timeout", cfg.ManifestFetchTimeout, "HTTP timeout per manifest fetch")
	fs.DurationVar(&cfg.MaxStale, "max-stale", cfg.MaxStale, "drop last-good after this duration")
	fs.StringVar(&cfg.StaticOverlayPath, "static-overlay", cfg.StaticOverlayPath, "path to nodes.yaml")
	fs.BoolVar(&cfg.RejectUnsigned, "reject-unsigned", cfg.RejectUnsigned, "reject unsigned manifests by default")

	// Publisher-only
	fs.StringVar(&cfg.KeystorePath, "keystore-path", cfg.KeystorePath, "V3 JSON keystore for orchestrator key (publisher only)")
	keystorePasswordFile := fs.String("keystore-password-file", "", "file containing keystore password (defaults to LIVEPEER_KEYSTORE_PASSWORD env)")
	fs.StringVar(&cfg.OrchAddress, "orch-address", cfg.OrchAddress, "explicit orchestrator address (defaults to keystore address)")
	fs.StringVar(&cfg.ManifestOut, "manifest-out", cfg.ManifestOut, "if set, write signed manifest JSON here on every SignManifest")
	fs.DurationVar(&cfg.WorkerProbeTimeout, "worker-probe-timeout", cfg.WorkerProbeTimeout, "HTTP timeout for ProbeWorker")

	// Metrics (both modes)
	fs.StringVar(&cfg.MetricsListen, "metrics-listen", cfg.MetricsListen, "host:port for the Prometheus /metrics HTTP listener; empty (default) disables it")
	fs.StringVar(&cfg.MetricsPath, "metrics-path", cfg.MetricsPath, "URL path for the metrics handler")
	fs.IntVar(&cfg.MetricsMaxSeriesPerMetric, "metrics-max-series-per-metric", cfg.MetricsMaxSeriesPerMetric, "max distinct label tuples per Prometheus metric vec; 0 disables the cap")

	if err := fs.Parse(args); err != nil {
		// flag returns ErrHelp on -h / --help.
		if err.Error() == "flag: help requested" {
			return nil, true, nil
		}
		return nil, false, err
	}

	cfg.Mode = config.Mode(strings.TrimSpace(*mode))
	cfg.Discovery = config.DiscoveryMode(strings.TrimSpace(*discovery))
	cfg.KeystorePassword = readPassword(*keystorePasswordFile)

	// Dev mode forces overlay-only discovery — no real chain to walk.
	if cfg.Dev {
		cfg.Discovery = config.DiscoveryOverlayOnly
	}

	// In --dev mode, suppress the default chain-rpc unless the operator
	// explicitly passed one. This keeps the validator from tripping on
	// the dev/chain-rpc mutual-exclusion when the user just wrote
	// `--mode=resolver --dev`.
	if cfg.Dev && !flagSet(fs, "chain-rpc") {
		cfg.ChainRPC = ""
	}

	if err := cfg.Validate(); err != nil {
		return nil, false, err
	}
	return cfg, false, nil
}

// flagSet reports whether name was explicitly set on the command line
// (vs. left at default).
func flagSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// usage prints flag help. Used when --help / -h is passed.
func usage() string {
	return strings.Join([]string{
		"livepeer-service-registry-daemon — registry sidecar for Livepeer orchestrator/worker discovery",
		"",
		"Usage:",
		"  livepeer-service-registry-daemon --mode=resolver --socket=/tmp/r.sock [flags]",
		"  livepeer-service-registry-daemon --mode=publisher --socket=/tmp/p.sock --keystore-path=... [flags]",
		"",
		"See docs/operations/running-the-daemon.md for the full flag reference.",
	}, "\n") + "\n"
}

// stderr is a tiny indirection so tests can swap the writer.
func stderr() *os.File { return os.Stderr }
