// Package seeder registers the active chain pool with the resolver on round
// events. Metadata and health refresh run independently in bounded workers.
package seeder

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/services/roundclock"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/discovery"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/resolver"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

// Seeder pairs discovery with the resolver candidate set on each round event.
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
	// Resolver receives the discovered candidate set without per-address I/O.
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
// On each round transition, the seeder enumerates and registers the active pool.
// Background workers refresh addresses independently. The first event arrives from
// chain-commons.timesource shortly after subscription, so the cache
// warms within one poll interval of daemon startup.
//
// Enumeration failures are logged and retried every 30 seconds without
// taking down the daemon; selection distinguishes incomplete discovery state.
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

	retry := time.NewTicker(30 * time.Second)
	defer retry.Stop()
	var pending bool
	var lastRound chain.Round
	for {
		select {
		case <-retry.C:
			if pending {
				pending = !s.refresh(ctx, lastRound)
			}
		case <-ctx.Done():
			s.log.Info("seeder: stopping (ctx done)")
			return nil
		case r, ok := <-rounds:
			if !ok {
				s.log.Info("seeder: round-event channel closed")
				return nil
			}
			lastRound = r
			pending = !s.refresh(ctx, r)
		}
	}
}

// refresh enumerates the pool within a deadline and registers its candidates.
func (s *Seeder) refresh(ctx context.Context, r chain.Round) bool {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	addrs, err := s.disc.ActiveOrchs(ctx)
	if err != nil {
		s.resolver.DiscoveryFailed()
		s.log.Warn("seeder: discovery failed", "round", r.Number, "err", err)
		return false
	}
	s.log.Info("seeder: refreshing cache", "round", r.Number, "orchs", len(addrs))

	addresses := make([]types.EthAddress, 0, len(addrs))
	for _, a := range addrs {
		addresses = append(addresses, types.EthAddress(a))
	}
	s.resolver.Discover(addresses)
	return true
}
