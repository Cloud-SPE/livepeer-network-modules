package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/repo/manifestcache"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/runtime/grpc"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/runtime/lifecycle"
	rmetrics "github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/runtime/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/runtime/seeder"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/service/publisher"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/service/resolver"
)

// run is the testable entrypoint — main() calls it with os.Args.
func run(ctx context.Context, args []string) error {
	cfg, helpAsked, err := parseFlags(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stderr, usage())
			return nil
		}
		return err
	}
	if helpAsked {
		fmt.Fprint(os.Stderr, usage())
		return nil
	}

	bp, err := build(ctx, cfg)
	if err != nil {
		return err
	}
	defer bp.Close()

	cacheRepo := manifestcache.WithMetrics(manifestcache.New(bp.store), bp.recorder)
	auditRepo := audit.WithMetrics(audit.New(bp.store), bp.recorder)

	srvCfg := grpc.Config{
		Cache:  cacheRepo,
		Audit:  auditRepo,
		Logger: bp.log,
	}
	var resolverSvc *resolver.Service
	switch cfg.Mode {
	case config.ModeResolver:
		resolverSvc = resolver.New(resolver.Config{
			Chain:            bp.chain,
			Fetcher:          bp.fetcher,
			Verifier:         bp.verify,
			Cache:            cacheRepo,
			Audit:            auditRepo,
			Overlay:          bp.overlayAccessor,
			Clock:            bp.clock,
			Logger:           bp.log,
			Recorder:         bp.recorder,
			CacheManifestTTL: cfg.CacheManifestTTL,
			MaxStale:         cfg.MaxStale,
			RejectUnsigned:   cfg.RejectUnsigned,
			LiveHealth:       bp.liveHealth,
		})
		srvCfg.Resolver = resolverSvc
	case config.ModePublisher:
		srvCfg.Publisher = publisher.New(publisher.Config{
			Signer:   bp.signer,
			Audit:    auditRepo,
			Clock:    bp.clock,
			Logger:   bp.log,
			Recorder: bp.recorder,
		})
	}

	srv, err := grpc.NewServer(srvCfg)
	if err != nil {
		return err
	}

	grpcListener, err := grpc.NewListener(grpc.ListenerConfig{
		SocketPath: cfg.SocketPath,
		Server:     srv,
		Logger:     bp.log,
		Recorder:   bp.recorder,
		Version:    version,
	})
	if err != nil {
		return err
	}

	metricsListener, err := rmetrics.NewListener(rmetrics.Config{
		Addr:     cfg.MetricsListen,
		Path:     cfg.MetricsPath,
		Recorder: bp.recorder,
		Logger:   bp.log,
	})
	if err != nil {
		return err
	}

	// Build the chain auto-discovery seeder when resolver+chain mode is
	// active. The seeder subscribes to round events and refreshes the
	// resolver cache on each transition.
	var seederSvc *seeder.Seeder
	if cfg.Mode == config.ModeResolver && cfg.Discovery == config.DiscoveryChain && bp.roundClock != nil {
		seederSvc, err = seeder.New(seeder.Config{
			Discovery: bp.discovery,
			Resolver:  resolverSvc,
			Clock:     bp.roundClock,
			Name:      "service-registry-resolver",
			Logger:    bp.log,
		})
		if err != nil {
			return err
		}
	}

	// Overlay-only resolver: walk the overlay once at startup so
	// ListKnown / Select return the operator-curated pool without a
	// per-consumer Refresh roundtrip. Each ResolveByAddress drops into
	// either the chain path (production overlay-only with a real RPC) or
	// the chainless static-overlay synth path (dev / static-overlay-only
	// example). Per-address errors are logged and swallowed.
	if cfg.Mode == config.ModeResolver && cfg.Discovery == config.DiscoveryOverlayOnly {
		seedOverlayCache(ctx, resolverSvc, bp.overlayAccessor(), bp.log)
	}

	bp.log.Info("daemon ready",
		"mode", string(cfg.Mode),
		"socket", cfg.SocketPath,
		"metrics_listen", cfg.MetricsListen,
		"discovery", string(cfg.Discovery),
		"version", version,
	)
	return lifecycle.Run(ctx, lifecycle.RunConfig{
		Server:          srv,
		Listener:        grpcListener,
		MetricsListener: metricsListener,
		Seeder:          seederSvc,
		Store:           bp.store,
		Logger:          bp.log,
	})
}

// seedOverlayCache calls ResolveByAddress once for each enabled overlay
// entry. Errors are non-fatal — a missing manifest for one address must
// not prevent the others from seeding.
func seedOverlayCache(ctx context.Context, r *resolver.Service, o *config.Overlay, log logger.Logger) {
	if r == nil || o == nil {
		return
	}
	for i := range o.Entries {
		e := &o.Entries[i]
		if !e.Enabled {
			continue
		}
		req := resolver.Request{
			Address:             e.EthAddress,
			AllowLegacyFallback: true,
			AllowUnsigned:       e.UnsignedAllowed,
		}
		if _, err := r.ResolveByAddress(ctx, req); err != nil {
			log.Warn("overlay-only seed: ResolveByAddress failed",
				"addr", e.EthAddress, "err", err)
		}
	}
}
