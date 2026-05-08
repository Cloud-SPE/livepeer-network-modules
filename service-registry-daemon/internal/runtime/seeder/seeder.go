// Package seeder runs the resolver's chain-side cache seeder. On each
// round transition (subscribed via chain-commons.services.roundclock),
// the seeder walks the active orchestrator pool via discovery and
// invokes ResolveByAddress with ForceRefresh=true for each address —
// warming the cache so subsequent gateway queries hit a fresh entry
// without doing the chain walk inline.
//
// One round event ≈ one ~2N+1 RPC burst (~201 calls for 100 orchs,
// once per ~19 hours on Arbitrum One). Mid-round queries hit the
// cache. Operators can hand-trigger Refresh() over gRPC for ad-hoc
// invalidation without waiting for the next round.
package seeder

import (
	"context"
	"errors"
	"sync"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/services/roundclock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/discovery"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/service/resolver"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

// Seeder pairs a Discovery with a Resolver, refreshing the resolver's
// cache on each Round event from the Clock.
type Seeder struct {
	disc     discovery.Discovery
	resolver *resolver.Service
	clock    roundclock.NamedClock
	log      logger.Logger
	name     string

	mu      sync.Mutex
	stopped bool
}

// Config wires the seeder.
type Config struct {
	// Discovery returns the active-orch set on each round event.
	Discovery discovery.Discovery
	// Resolver receives ResolveByAddress(ForceRefresh=true) for each
	// discovered address.
	Resolver *resolver.Service
	// Clock provides the named-subscription round-event channel.
	Clock roundclock.NamedClock
	// Name is the persistent subscription name passed to
	// SubscribeRoundsForName. Survives daemon restart so dedup works
	// across restarts.
	Name   string
	Logger logger.Logger
}

// New constructs a Seeder. Validation only — no I/O.
func New(c Config) (*Seeder, error) {
	if c.Discovery == nil {
		return nil, errors.New("seeder: Discovery is required")
	}
	if c.Resolver == nil {
		return nil, errors.New("seeder: Resolver is required")
	}
	if c.Clock == nil {
		return nil, errors.New("seeder: Clock is required")
	}
	if c.Name == "" {
		c.Name = "service-registry-resolver"
	}
	if c.Logger == nil {
		c.Logger = logger.Discard()
	}
	return &Seeder{
		disc:     c.Discovery,
		resolver: c.Resolver,
		clock:    c.Clock,
		log:      c.Logger,
		name:     c.Name,
	}, nil
}

// Run blocks until ctx is canceled or the round-event channel closes.
// On each round transition, the seeder walks Discovery and refreshes
// each address in the resolver cache. The first event arrives from
// chain-commons.timesource shortly after subscription, so the cache
// warms within one poll interval of daemon startup.
//
// Errors from Discovery or per-address resolves are logged and
// swallowed — a single transient failure shouldn't take down the
// seeder loop. Persistent failures will be visible in the
// resolver/chain Recorder counters.
func (s *Seeder) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return errors.New("seeder: already stopped")
	}
	s.mu.Unlock()

	rounds, err := s.clock.SubscribeRoundsForName(ctx, s.name)
	if err != nil {
		return err
	}
	s.log.Info("seeder: subscribed to round events", "name", s.name)

	for {
		select {
		case <-ctx.Done():
			s.log.Info("seeder: stopping (ctx done)")
			return nil
		case r, ok := <-rounds:
			if !ok {
				s.log.Info("seeder: round-event channel closed")
				return nil
			}
			s.refresh(ctx, r)
		}
	}
}

// refresh walks discovery + force-refreshes each entry in the
// resolver cache. Best-effort — per-address errors are logged and
// loop continues.
func (s *Seeder) refresh(ctx context.Context, r chain.Round) {
	addrs, err := s.disc.ActiveOrchs(ctx)
	if err != nil {
		s.log.Warn("seeder: discovery failed", "round", r.Number, "err", err)
		return
	}
	s.log.Info("seeder: refreshing cache", "round", r.Number, "orchs", len(addrs))

	for _, a := range addrs {
		// Per-address ResolveByAddress with ForceRefresh re-reads the
		// chain serviceURI + re-fetches the manifest + verifies
		// signature, then writes the cache entry. AllowLegacyFallback
		// matches the existing Refresh() gRPC behavior.
		req := resolver.Request{
			Address:             types.EthAddress(a),
			ForceRefresh:        true,
			AllowLegacyFallback: true,
		}
		if _, err := s.resolver.ResolveByAddress(ctx, req); err != nil {
			s.log.Debug("seeder: resolve failed", "addr", a, "err", err)
			continue
		}
	}
}
