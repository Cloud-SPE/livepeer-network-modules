package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	ethcommon "github.com/ethereum/go-ethereum/common"

	cchain "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	cclock "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/clock"
	cccontrollerapi "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/controller"
	cccontroller "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/controller/eth"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/keystore/v3json"
	ccrpcmulti "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/rpc/multi"
	cstorebolt "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/store/bolt"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/timesource/poller"
	ccroundclock "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/roundclock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/clock"
	clockadapter "github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/clock/chaincommonsadapter"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/discovery"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/livehealthfetcher"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/manifestfetcher"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/signer"
	signeradapter "github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/signer/chaincommonsadapter"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/store"
	storeadapter "github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/store/chaincommonsadapter"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/verifier"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// builtProviders holds the set of providers needed by the services. The
// resolver needs all I/O providers; the publisher needs Signer + Chain.
type builtProviders struct {
	cfg      *config.Daemon
	log      logger.Logger
	store    store.Store
	chain    chain.Chain
	signer   signer.Signer
	verify   verifier.Verifier
	fetcher  manifestfetcher.ManifestFetcher
	liveHealth livehealthfetcher.Fetcher
	clock    clock.Clock
	recorder metrics.Recorder

	// Resolver chain-discovery dependencies (nil unless in resolver
	// mode with --discovery=chain). roundclock + discovery feed the
	// runtime/seeder loop.
	roundClock ccroundclock.NamedClock
	discovery  discovery.Discovery

	// closers are extra teardown callbacks that aren't covered by
	// store.Close (chain-commons RPC client, Controller refresher,
	// timesource poller). Drained in reverse on shutdown.
	closers []func()

	// overlayLoader returns the current static overlay; reload swaps the
	// pointer atomically so resolver reads see the new value next call.
	overlay atomic.Pointer[config.Overlay]
}

func (bp *builtProviders) addCloser(fn func()) {
	bp.closers = append(bp.closers, fn)
}

// build assembles providers from cfg. Dev mode uses fakes; production
// dials chain RPC and loads the keystore.
func build(ctx context.Context, cfg *config.Daemon) (*builtProviders, error) {
	// Clock: chain-commons-backed via thin adapter.
	clk, err := clockadapter.New(cclock.System())
	if err != nil {
		return nil, fmt.Errorf("providers: clock adapter: %w", err)
	}
	bp := &builtProviders{
		cfg:    cfg,
		clock:  clk,
		verify: verifier.New(),
	}
	bp.log = logger.New(logger.Config{Level: cfg.LogLevel, Format: cfg.LogFormat})

	// Recorder: Prometheus when --metrics-listen is set, Noop otherwise.
	// The runtime/metrics listener is created in run.go; here we just
	// pick the implementation so providers can register on it.
	if cfg.MetricsListen != "" {
		bp.recorder = metrics.NewPrometheus(metrics.PrometheusConfig{
			MaxSeriesPerMetric: cfg.MetricsMaxSeriesPerMetric,
			OnCapExceeded: func(name string, observed, capLimit int) {
				bp.log.Warn("metric cardinality cap exceeded; new label tuples dropped",
					"metric", name, "observed", observed, "cap", capLimit)
			},
		})
	} else {
		bp.recorder = metrics.NewNoop()
	}

	if cfg.Dev {
		fmt.Fprintln(os.Stderr, "=== DEV MODE — in-memory providers, throwaway keys, no chain ===")
	}

	// Store: chain-commons-backed for production (BoltDB) and dev (in-memory),
	// adapted to the registry-local Store interface.
	if cfg.Dev {
		// Dev mode keeps the registry-local in-memory store — its semantics
		// match chain-commons' but we avoid pulling chain-commons.testing
		// (a test-only package) into the production binary.
		bp.store = store.NewMemory()
	} else {
		bs, err := cstorebolt.Open(cfg.StorePath, cstorebolt.Default())
		if err != nil {
			return nil, fmt.Errorf("providers: store bolt: %w", err)
		}
		adapter, err := storeadapter.New(bs)
		if err != nil {
			return nil, fmt.Errorf("providers: store adapter: %w", err)
		}
		bp.store = adapter
	}

	// Signer (publisher only). Production: chain-commons V3 JSON keystore
	// behind the registry-local Signer interface. Dev: registry-local
	// random keystore (avoids pulling chain-commons.testing into prod
	// binaries; the dev path doesn't exercise the adapter but the adapter
	// has its own tests).
	if cfg.Mode == config.ModePublisher {
		if cfg.Dev {
			sk, err := signer.GenerateRandom()
			if err != nil {
				return nil, fmt.Errorf("providers: signer (dev): %w", err)
			}
			bp.signer = sk
		} else {
			ks, err := v3json.Open(cfg.KeystorePath, cfg.KeystorePassword, cchain.Address{})
			if err != nil {
				return nil, fmt.Errorf("providers: signer keystore: %w", err)
			}
			sa, err := signeradapter.New(ks)
			if err != nil {
				return nil, fmt.Errorf("providers: signer adapter: %w", err)
			}
			bp.signer = sa
		}
	}

	var controllerAddrs cccontrollerapi.Addresses

	// Resolver production deployments resolve ServiceRegistry from the
	// Controller by default so operators don't need to pass the address
	// explicitly. The explicit flag remains as an override.
	if cfg.Mode == config.ModeResolver && !cfg.Dev {
		ccRPC, err := ccrpcmulti.Open(ccrpcmulti.Options{URLs: []string{cfg.ChainRPC}})
		if err != nil {
			return nil, fmt.Errorf("providers: chain-commons rpc: %w", err)
		}
		bp.addCloser(func() { _ = ccRPC.Close() })

		ctrl, err := cccontroller.New(ctx, cccontroller.Options{
			RPC:             ccRPC,
			ControllerAddr:  ethcommon.HexToAddress(cfg.ControllerAddress),
			RefreshInterval: 1 * time.Hour,
		})
		if err != nil {
			return nil, fmt.Errorf("providers: chain-commons controller: %w", err)
		}
		bp.addCloser(func() {
			if c, ok := ctrl.(io.Closer); ok {
				_ = c.Close()
			}
		})
		controllerAddrs = ctrl.Addresses()

		// Resolver chain-discovery: use the same controller + RPC wiring
		// for round polling and pool walking when chain discovery is on.
		if cfg.Discovery == config.DiscoveryChain {
			ts, err := poller.New(poller.Options{
				RPC:          ccRPC,
				Controller:   ctrl,
				PollInterval: cfg.RoundPollInterval,
			})
			if err != nil {
				return nil, fmt.Errorf("providers: chain-commons timesource: %w", err)
			}
			bp.addCloser(func() {
				if c, ok := ts.(io.Closer); ok {
					_ = c.Close()
				}
			})

			rc, err := ccroundclock.New(ccroundclock.Options{
				TimeSource: ts,
			})
			if err != nil {
				return nil, fmt.Errorf("providers: chain-commons roundclock: %w", err)
			}
			named, ok := rc.(ccroundclock.NamedClock)
			if !ok {
				return nil, fmt.Errorf("providers: roundclock impl does not satisfy NamedClock")
			}
			bp.roundClock = named

			disc, err := discovery.NewChain(ccRPC, controllerAddrs.BondingManager)
			if err != nil {
				return nil, fmt.Errorf("providers: discovery: %w", err)
			}
			bp.discovery = disc
		} else {
			bp.discovery = discovery.NewDisabled()
		}
	}

	// Chain
	if cfg.Dev {
		// In dev mode, resolver uses an empty in-memory chain (callers
		// can preload via tests / examples). Publisher uses one keyed by
		// its own signing address.
		var addr types.EthAddress
		if bp.signer != nil {
			addr = bp.signer.Address()
		}
		bp.chain = chain.NewInMemory(addr)
	} else if cfg.Mode == config.ModeResolver {
		cli, err := ethclient.DialContext(ctx, cfg.ChainRPC)
		if err != nil {
			return nil, fmt.Errorf("providers: chain dial %s: %w", cfg.ChainRPC, err)
		}
		serviceRegistryAddress := cfg.ServiceRegistryAddress
		if serviceRegistryAddress == "" && cfg.Mode == config.ModeResolver {
			serviceRegistryAddress = controllerAddrs.ServiceRegistry.Hex()
		}
		eth, err := chain.NewEth(chain.EthConfig{
			Client:                   cli,
			ServiceRegistryAddress:   serviceRegistryAddress,
			AIServiceRegistryAddress: cfg.AIServiceRegistryAddress,
		})
		if err != nil {
			return nil, fmt.Errorf("providers: chain: %w", err)
		}
		bp.chain = eth
	} else {
		// Publisher mode doesn't read on-chain serviceURI pointers.
		bp.chain = chain.NewInMemory("")
	}
	bp.chain = chain.WithMetrics(bp.chain, bp.recorder)

	// Manifest fetcher (resolver only, but cheap to always build)
	bp.fetcher = manifestfetcher.WithMetrics(
		manifestfetcher.New(manifestfetcher.Config{
			MaxBytes:      cfg.ManifestMaxBytes,
			Timeout:       cfg.ManifestFetchTimeout,
			AllowInsecure: cfg.Dev,
		}),
		bp.recorder,
	)
	bp.liveHealth = livehealthfetcher.New(cfg.WorkerProbeTimeout)

	// Static overlay (resolver only)
	if cfg.StaticOverlayPath != "" {
		raw, err := os.ReadFile(cfg.StaticOverlayPath) //nolint:gosec // operator-supplied path
		if err != nil {
			bp.recorder.IncOverlayReload(metrics.OutcomeIOError)
			return nil, fmt.Errorf("providers: overlay read: %w", err)
		}
		o, err := config.ParseOverlayYAML(raw)
		if err != nil {
			bp.recorder.IncOverlayReload(metrics.OutcomeParseError)
			return nil, fmt.Errorf("providers: overlay parse: %w", err)
		}
		bp.overlay.Store(o)
		bp.recorder.IncOverlayReload(metrics.OutcomeOK)
		bp.recorder.SetOverlayEntries(len(o.Entries))
	} else {
		bp.overlay.Store(config.EmptyOverlay())
	}

	if cfg.Mode != config.ModeResolver || cfg.Dev {
		// Overlay-only / publisher / dev: discovery is the no-op.
		bp.discovery = discovery.NewDisabled()
	}

	// Stamp build info — reflects in /metrics for dashboard panels.
	bp.recorder.SetBuildInfo("dev", string(cfg.Mode), runtimeGoVersion())
	return bp, nil
}

// runtimeGoVersion returns the runtime Go version. Pulled into a
// helper because runtime.Version() is one indirection nobody needs
// to read inline.
func runtimeGoVersion() string { return runtime.Version() }

// overlayAccessor is the resolver-side lookup function.
func (bp *builtProviders) overlayAccessor() *config.Overlay {
	return bp.overlay.Load()
}

// Close releases provider resources.
func (bp *builtProviders) Close() {
	// Chain-commons closers (rpc, controller, timesource) — LIFO order.
	for i := len(bp.closers) - 1; i >= 0; i-- {
		bp.closers[i]()
	}
	if bp.store != nil {
		_ = bp.store.Close()
	}
	if k, ok := bp.signer.(*signer.Keystore); ok {
		k.Close()
	}
}
