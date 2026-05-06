// Command livepeer-orch-coordinator scrapes capability-broker
// /registry/offerings on the operator's LAN, builds candidate
// manifests, hosts them for hand-carry to secure-orch-console,
// receives the cold-key-signed manifest back, and atomic-swap
// publishes at /.well-known/livepeer-registry.json on a separate
// public-facing listener.
//
// See ../../docs/operator-runbook.md for what each flag means and
// what each failure mode looks like in production.
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
	"strings"
	"syscall"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/providers/brokerclient"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/candidates"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/published"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/server/adminapi"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/candidate"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/receive"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/scrape"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/types"
)

var version = "dev"

const configErrExitCode = 2

func main() {
	var (
		listenAddr    = flag.String("listen", ":8080", "operator UX HTTP listener (LAN-bound by intent; web UI + JSON API + signed-manifest upload)")
		publicListen  = flag.String("public-listen", ":8081", "resolver-facing listener; serves only GET /.well-known/livepeer-registry.json")
		metricsListen = flag.String("metrics-listen", ":9091", "Prometheus metrics listener")

		configPath = flag.String("config", "/etc/livepeer/orch-coordinator.yaml", "path to coordinator-config.yaml")
		dataDir    = flag.String("data-dir", "/var/lib/livepeer/orch-coordinator", "filesystem root for candidate snapshots, audit log, and the published manifest")

		scrapeInterval  = flag.Duration("scrape-interval", 30*time.Second, "broker poll cadence")
		scrapeTimeout   = flag.Duration("scrape-timeout", 5*time.Second, "per-broker scrape timeout")
		freshnessWindow = flag.Duration("freshness-window", 150*time.Second, "drop-stale-tuples threshold (default 5 × scrape-interval)")
		manifestTTL     = flag.Duration("manifest-ttl", 24*time.Hour, "expires_at = issued_at + manifest_ttl")

		dev      = flag.Bool("dev", false, "dev mode: synthetic fake-broker fixtures; loud DEV MODE banner")
		logLevel = flag.String("log-level", "info", "slog level: debug|info|warn|error")
		logFmt   = flag.String("log-format", "text", "slog format: text|json")

		showVer = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVer {
		fmt.Println(version)
		return
	}

	logger := buildLogger(*logLevel, *logFmt)

	cfg := bootConfig{
		listenAddr:      *listenAddr,
		publicListen:    *publicListen,
		metricsListen:   *metricsListen,
		configPath:      *configPath,
		dataDir:         *dataDir,
		scrapeInterval:  *scrapeInterval,
		scrapeTimeout:   *scrapeTimeout,
		freshnessWindow: *freshnessWindow,
		manifestTTL:     *manifestTTL,
		dev:             *dev,
	}

	if err := run(logger, cfg); err != nil {
		var cfgErr *configError
		if errors.As(err, &cfgErr) {
			logger.Error("config error", "err", cfgErr.Unwrap())
			os.Exit(configErrExitCode)
		}
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

type bootConfig struct {
	listenAddr      string
	publicListen    string
	metricsListen   string
	configPath      string
	dataDir         string
	scrapeInterval  time.Duration
	scrapeTimeout   time.Duration
	freshnessWindow time.Duration
	manifestTTL     time.Duration
	dev             bool
}

type configError struct{ err error }

func (e *configError) Error() string { return e.err.Error() }
func (e *configError) Unwrap() error { return e.err }

func run(logger *slog.Logger, cfg bootConfig) error {
	if cfg.dev {
		fmt.Fprintln(os.Stderr, "=== DEV MODE === livepeer-orch-coordinator: synthetic fake-broker fixtures; do not deploy to production")
	}

	if err := os.MkdirAll(cfg.dataDir, 0o755); err != nil {
		return fmt.Errorf("prepare data dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.dataDir, "candidates"), 0o755); err != nil {
		return fmt.Errorf("prepare candidates dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.dataDir, "published"), 0o755); err != nil {
		return fmt.Errorf("prepare published dir: %w", err)
	}

	loaded, err := loadCoordinatorConfig(cfg)
	if err != nil {
		return &configError{err: err}
	}

	logger.Info("livepeer-orch-coordinator starting",
		"version", version,
		"listen", cfg.listenAddr,
		"public_listen", cfg.publicListen,
		"metrics_listen", cfg.metricsListen,
		"data_dir", cfg.dataDir,
		"orch_eth_address", loaded.EthAddress(),
		"brokers", brokerNames(loaded.Brokers),
		"dev", cfg.dev,
	)

	ttl := loaded.Publish.ManifestTTL
	if ttl <= 0 {
		ttl = cfg.manifestTTL
	}

	var client brokerclient.Client
	if cfg.dev {
		client = newDevFake(loaded.EthAddress(), loaded.Brokers)
	} else {
		client = brokerclient.New(cfg.scrapeTimeout)
	}

	scrapeSvc, err := scrape.New(scrape.Config{
		OrchEthAddress:  loaded.EthAddress(),
		Brokers:         loaded.Brokers,
		ScrapeInterval:  cfg.scrapeInterval,
		ScrapeTimeout:   cfg.scrapeTimeout,
		FreshnessWindow: cfg.freshnessWindow,
	}, client, logger.With("component", "scrape"))
	if err != nil {
		return &configError{err: err}
	}

	candStore, err := candidates.New(filepath.Join(cfg.dataDir, "candidates"), 0)
	if err != nil {
		return fmt.Errorf("candidate store: %w", err)
	}

	builder, err := candidate.NewBuilder(scrapeSvc, candStore, candidate.BuildOptions{
		OrchEthAddress:    loaded.EthAddress(),
		ManifestTTL:       ttl,
		CoordinatorCommit: version,
	}, logger.With("component", "candidate"))
	if err != nil {
		return fmt.Errorf("candidate builder: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go scrapeSvc.Run(ctx)
	go runBuilder(ctx, builder, cfg.scrapeInterval, logger.With("component", "candidate"))

	publishedStore, err := published.New(filepath.Join(cfg.dataDir, "published"))
	if err != nil {
		return fmt.Errorf("published store: %w", err)
	}
	auditLog, err := audit.Open(filepath.Join(cfg.dataDir, "audit.db"))
	if err != nil {
		return fmt.Errorf("audit log: %w", err)
	}
	defer auditLog.Close()

	receiveSvc := receive.New(publishedStore, auditLog, loaded.EthAddress(), candidate.SpecVersion)

	admin := adminapi.New(cfg.listenAddr, logger.With("component", "adminapi"))
	admin.CandidateRoutes(builder, candStore)
	admin.UploadRoutes(receiveSvc)
	if _, err := admin.Listen(); err != nil {
		return fmt.Errorf("admin listen: %w", err)
	}
	logger.Info("admin listener bound", "addr", admin.Addr())
	go func() {
		if err := admin.Serve(ctx); err != nil {
			logger.Error("admin serve", "err", err)
		}
	}()

	<-ctx.Done()
	logger.Info("shutdown signal received")
	return nil
}

func runBuilder(ctx context.Context, b *candidate.Builder, interval time.Duration, logger *slog.Logger) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	// Wait one scrape cycle before the first build so the cache is warm.
	first := time.NewTimer(interval)
	defer first.Stop()
	select {
	case <-ctx.Done():
		return
	case <-first.C:
	}
	if _, err := b.Rebuild(); err != nil {
		logger.Warn("initial candidate build", "err", err)
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := b.Rebuild(); err != nil {
				logger.Warn("rebuild", "err", err)
			}
		}
	}
}

// loadCoordinatorConfig loads from disk in production mode. In dev
// mode, if the file is missing, a synthetic in-memory config is used.
func loadCoordinatorConfig(cfg bootConfig) (*config.Config, error) {
	if cfg.dev {
		if _, err := os.Stat(cfg.configPath); err != nil {
			return synthDevConfig(), nil
		}
	}
	return config.Load(cfg.configPath)
}

func synthDevConfig() *config.Config {
	return &config.Config{
		Identity: config.Identity{OrchEthAddress: "0x" + strings.Repeat("ab", 20)},
		Brokers: []config.Broker{
			{Name: "fake-broker-a", BaseURL: "http://fake-a.lan:8080"},
			{Name: "fake-broker-b", BaseURL: "http://fake-b.lan:8080"},
		},
		Publish: config.Publish{ManifestTTL: 24 * time.Hour},
	}
}

func newDevFake(orchAddr string, brokers []config.Broker) brokerclient.Client {
	f := brokerclient.NewFake()
	for i, b := range brokers {
		caps := []types.BrokerOffering{{
			CapabilityID:    "demo:echo:v1",
			OfferingID:      "default",
			InteractionMode: "http-reqresp@v0",
			WorkUnit:        types.WorkUnit{Name: "echoes"},
			PricePerUnitWei: fmt.Sprintf("%d", 100+i*10),
		}}
		f.Set(b.BaseURL, &types.BrokerOfferings{
			OrchEthAddress: orchAddr,
			Capabilities:   caps,
		}, nil)
	}
	return f
}

func brokerNames(brokers []config.Broker) []string {
	out := make([]string, 0, len(brokers))
	for _, b := range brokers {
		out = append(out, b.Name)
	}
	return out
}

func buildLogger(level, format string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	if strings.ToLower(format) == "json" {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}
