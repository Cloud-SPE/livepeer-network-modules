package config

import (
	"fmt"
	"time"
)

// Mode is the daemon's operating mode. One binary, two modes.
type Mode string

const (
	ModePublisher Mode = "publisher"
	ModeResolver  Mode = "resolver"
)

// DiscoveryMode picks the resolver's seeding source for ListKnown /
// Select. "chain" walks the BondingManager active pool on each round
// event (via chain-commons.services.roundclock); "overlay-only" leaves
// the cache fed only by --static-overlay + per-address gRPC calls.
type DiscoveryMode string

const (
	// DiscoveryChain walks BondingManager.GetFirstTranscoderInPool +
	// GetNextTranscoderInPool on each chain-commons.roundclock event.
	// Default for production deployments.
	DiscoveryChain DiscoveryMode = "chain"

	// DiscoveryOverlayOnly disables chain auto-discovery; cache fed
	// only by static overlay + per-addr ResolveByAddress. Operator
	// pick for strict allowlisting.
	DiscoveryOverlayOnly DiscoveryMode = "overlay-only"
)

// Daemon is the validated daemon-level configuration. Built by the
// runtime layer from CLI flags. Fields are immutable once built; a
// reload (overlay change) creates a new struct.
type Daemon struct {
	Mode       Mode
	SocketPath string
	StorePath  string
	ChainRPC   string
	ChainID    int64
	// ControllerAddress is the Livepeer Controller address; used to
	// resolve BondingManager + RoundsManager for resolver chain
	// discovery and, by default, the ServiceRegistry contract address
	// for resolver GetServiceURI() lookups. Default Arbitrum One.
	ControllerAddress        string
	ServiceRegistryAddress   string
	AIServiceRegistryAddress string
	LogFormat                string
	LogLevel                 string
	Dev                      bool

	// Resolver-only:
	// Discovery picks the cache seeding source. See DiscoveryMode.
	Discovery            DiscoveryMode
	CacheManifestTTL     time.Duration
	ManifestMaxBytes     int64
	ManifestFetchTimeout time.Duration
	MaxStale             time.Duration
	StaticOverlayPath    string
	RejectUnsigned       bool

	// RoundPollInterval governs how often the chain-commons timesource
	// polls RoundsManager.currentRound() to detect round transitions
	// (resolver mode + Discovery=chain). Round transitions trigger the
	// pool re-walk; this interval bounds detection latency. Default 1
	// minute is plenty given ~19h rounds on Arbitrum One.
	RoundPollInterval time.Duration

	// Publisher-only:
	KeystorePath       string
	KeystorePassword   string
	OrchAddress        string
	ManifestOut        string
	WorkerProbeTimeout time.Duration

	// Metrics (both modes):
	MetricsListen             string // host:port; empty disables the listener
	MetricsPath               string // default /metrics
	MetricsMaxSeriesPerMetric int    // 0 disables the cap
}

// DefaultDaemon returns the default configuration before flags are
// applied. Used as the base struct that flag parsing mutates.
func DefaultDaemon() *Daemon {
	return &Daemon{
		SocketPath:                "/var/run/livepeer-service-registry.sock",
		StorePath:                 "/var/lib/livepeer/registry-cache.db",
		ChainRPC:                  "https://arb1.arbitrum.io/rpc",
		ChainID:                   42161,
		ControllerAddress:         "0xD8E8328501E9645d16Cf49539efC04f734606ee4", // Livepeer Controller, Arbitrum One
		ServiceRegistryAddress:    "",                                           // empty means resolve via Controller
		AIServiceRegistryAddress:  "0x04C0b249740175999E5BF5c9ac1dA92431EF34C5", // AI service registry, Arbitrum One
		LogFormat:                 "text",
		LogLevel:                  "info",
		Discovery:                 DiscoveryChain,
		RoundPollInterval:         1 * time.Minute,
		CacheManifestTTL:          10 * time.Minute,
		ManifestMaxBytes:          4 * 1024 * 1024,
		ManifestFetchTimeout:      5 * time.Second,
		MaxStale:                  1 * time.Hour,
		WorkerProbeTimeout:        5 * time.Second,
		RejectUnsigned:            true,
		MetricsPath:               "/metrics",
		MetricsMaxSeriesPerMetric: 10000,
	}
}

// Validate checks invariants. Returns a single descriptive error
// describing all problems encountered.
func (d *Daemon) Validate() error {
	if d.Mode != ModePublisher && d.Mode != ModeResolver {
		return fmt.Errorf("config: --mode must be %q or %q, got %q", ModePublisher, ModeResolver, d.Mode)
	}
	if d.SocketPath == "" {
		return fmt.Errorf("config: --socket is required")
	}
	if d.Dev && d.ChainRPC != "" && d.ChainRPC != "dev" {
		return fmt.Errorf("config: --dev and --chain-rpc are mutually exclusive")
	}
	if d.Mode == ModeResolver {
		switch d.Discovery {
		case DiscoveryChain, DiscoveryOverlayOnly:
		default:
			return fmt.Errorf("config: --discovery must be %q or %q, got %q",
				DiscoveryChain, DiscoveryOverlayOnly, d.Discovery)
		}
		if d.RoundPollInterval <= 0 {
			return fmt.Errorf("config: --round-poll-interval must be > 0")
		}
		if d.CacheManifestTTL <= 0 {
			return fmt.Errorf("config: --cache-manifest-ttl must be > 0")
		}
		if d.ManifestMaxBytes < 1024 {
			return fmt.Errorf("config: --manifest-max-bytes must be >= 1024 (got %d)", d.ManifestMaxBytes)
		}
		if d.ManifestMaxBytes > 16*1024*1024 {
			return fmt.Errorf("config: --manifest-max-bytes capped at 16 MiB (got %d)", d.ManifestMaxBytes)
		}
		if d.ManifestFetchTimeout <= 0 {
			return fmt.Errorf("config: --manifest-fetch-timeout must be > 0")
		}
	}
	if d.Mode == ModePublisher && !d.Dev {
		if d.KeystorePath == "" {
			return fmt.Errorf("config: --keystore-path is required in publisher mode")
		}
		if d.KeystorePassword == "" {
			return fmt.Errorf("config: keystore password (via --keystore-password-file or LIVEPEER_KEYSTORE_PASSWORD env) is required in publisher mode")
		}
	}
	return nil
}
