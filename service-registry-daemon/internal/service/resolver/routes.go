package resolver

import (
	"context"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/repo/manifestcache"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/selection"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

const refreshTimeout = 5 * time.Second
const refreshWorkers = 4

type tupleKey struct{ capability, offering string }

func tuple(c, o string) tupleKey { return tupleKey{strings.ToLower(c), strings.ToLower(o)} }

type addressState struct {
	entry       *manifestcache.Entry
	nodes       []types.ResolvedNode
	overlay     *config.Overlay
	until, next time.Time
	negative    bool
	loaded      bool
	status      types.DiscoveryStatus
	sourceNext  time.Time
}
type healthState struct {
	tuples      map[tupleKey]types.RouteHealthCapability
	until, next time.Time
	failures    int
}
type indexedRoute struct {
	node               types.ResolvedNode
	until              time.Time
	since              time.Time
	verifiedAt         time.Time
	healthUntil        time.Time
	healthKnown, ready bool
}
type routeSnapshot struct {
	overlay            *config.Overlay
	at                 time.Time
	discoveredAt       time.Time
	scopeAuthoritative bool
	known              []KnownAddress
	index              map[tupleKey][]indexedRoute
	coverageUntil      time.Time
	coverageSince      time.Time
	complete           bool
}
type routeState struct {
	mu           sync.Mutex
	flights      map[types.EthAddress]*refreshFlight
	discoveredAt time.Time
	addresses    map[types.EthAddress]addressState
	health       map[string]healthState
	locks        map[types.EthAddress]chan struct{}
	running      map[string]bool
	snapshot     atomic.Pointer[routeSnapshot]
	discovered   bool
	discoveryOK  bool
	candidateOK  bool
	pool         map[types.EthAddress]bool
	poolSet      bool
}

func newRouteState() *routeState {
	return &routeState{flights: map[types.EthAddress]*refreshFlight{}, addresses: map[types.EthAddress]addressState{}, health: map[string]healthState{}, locks: map[types.EthAddress]chan struct{}{}, running: map[string]bool{}, candidateOK: true}
}
func (s *Service) requestOverlay(req Request) *config.Overlay {
	if req.overlay != nil {
		return req.overlay
	}
	return s.overlay()
}
func (s *Service) lockAddress(ctx context.Context, addr types.EthAddress) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.routes.mu.Lock()
	ch := s.routes.locks[addr]
	if ch == nil {
		ch = make(chan struct{}, 1)
		s.routes.locks[addr] = ch
	}
	s.routes.mu.Unlock()
	select {
	case ch <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-ch
			return nil, err
		}
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func earlier(a, b time.Time) time.Time {
	if a.IsZero() || b.Before(a) {
		return b
	}
	return a
}
func retryDelay(failures int) time.Duration {
	if failures > 6 {
		failures = 6
	}
	return time.Duration(1<<uint(failures-1)) * time.Second
}
func refreshAt(now, until time.Time, key string) time.Time {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	// Refresh at 65–75% of remaining validity; deterministic jitter per endpoint.
	d := until.Sub(now) * time.Duration(65+h.Sum32()%11) / 100
	if d < 100*time.Millisecond {
		d = 100 * time.Millisecond
	}
	return now.Add(d)
}

// RefreshHealth fetches one broker response for all tuples. Concurrent callers
// share the in-flight work by skipping an already running endpoint.
func (s *Service) RefreshHealth(ctx context.Context, url string) {
	if s.liveHealth == nil {
		return
	}
	s.routes.mu.Lock()
	key := "health:" + url
	if s.routes.running[key] {
		s.routes.mu.Unlock()
		return
	}
	s.routes.running[key] = true
	s.routes.mu.Unlock()
	defer func() { s.routes.mu.Lock(); delete(s.routes.running, key); s.routes.mu.Unlock() }()
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	snap, err := s.liveHealth.Fetch(ctx, url)
	now := s.clock.Now()
	s.routes.mu.Lock()
	defer s.routes.mu.Unlock()
	old := s.routes.health[url]
	if err != nil || snap == nil {
		s.log.Debug("resolver background health refresh failed", "worker_url", url, "err", err)
		old.failures++
		old.next = now.Add(retryDelay(old.failures))
		s.routes.health[url] = old
	} else {
		h := healthState{tuples: map[tupleKey]types.RouteHealthCapability{}, until: now.Add(refreshTimeout)}
		for _, c := range snap.Capabilities {
			// Never let a remote endpoint extend our local observation bound indefinitely.
			if !c.StaleAfter.IsZero() {
				c.StaleAfter = earlier(c.StaleAfter, now.Add(refreshTimeout))
			}
			h.tuples[tuple(c.ID, c.OfferingID)] = c
			h.until = earlier(h.until, c.StaleAfter)
		}
		h.next = refreshAt(now, h.until, url)
		s.routes.health[url] = h
	}
	s.publishLocked()
}

// publishLocked constructs an immutable index. No outbound work is performed
// under this mutex and readers never acquire it.
func (s *Service) publishLocked() {
	ov := s.overlay()
	snap := &routeSnapshot{at: s.clock.Now(), discoveredAt: s.routes.discoveredAt, scopeAuthoritative: s.routes.discovered && s.routes.discoveryOK && s.routes.candidateOK, overlay: ov, index: map[tupleKey][]indexedRoute{}, complete: s.routes.discovered && s.routes.discoveryOK && s.routes.candidateOK}
	addrs := make([]string, 0, len(s.routes.addresses))
	for a := range s.routes.addresses {
		addrs = append(addrs, string(a))
	}
	sort.Strings(addrs)
	for _, a := range addrs {
		addr := types.EthAddress(a)
		st := s.routes.addresses[addr]
		e, configured := ov.FindByAddress(addr)
		if s.routes.poolSet && !s.routes.pool[addr] && (!configured || !e.Enabled) {
			continue
		}
		k := KnownAddress{Address: addr, Status: st.status, Until: st.until, HasEntry: st.entry != nil, Negative: st.negative}
		if k.Status.Compatibility == "" {
			k.Status = unknownStatus()
		}
		if st.entry != nil {
			k.Mode = st.entry.Mode
			k.CachedAt = st.entry.FetchedAt
		}
		snap.known = append(snap.known, k)
		if st.overlay != ov || (st.entry == nil && !st.negative) {
			snap.complete = false
			continue
		}
		snap.coverageUntil = earlier(snap.coverageUntil, st.until)
		if st.negative && st.status.CompatibilityCheckedAt.After(snap.coverageSince) {
			snap.coverageSince = st.status.CompatibilityCheckedAt
		}
		if st.entry != nil && st.entry.Manifest != nil && st.entry.Manifest.IssuedAt.After(snap.coverageSince) {
			snap.coverageSince = st.entry.Manifest.IssuedAt
		}
		for _, n := range st.nodes {
			for _, c := range n.Capabilities {
				for _, o := range c.Offerings {
					scoped := n
					cap := c
					cap.Offerings = []types.Offering{o}
					scoped.Capabilities = []types.Capability{cap}
					r := indexedRoute{verifiedAt: st.status.LastVerifiedAt, node: scoped, until: st.until, healthKnown: s.liveHealth == nil, ready: s.liveHealth == nil, healthUntil: st.until}
					if st.entry.Manifest != nil {
						r.since = st.entry.Manifest.IssuedAt
					}
					if s.liveHealth != nil {
						h := s.routes.health[n.URL]
						hc, ok := h.tuples[tuple(c.Name, o.ID)]
						r.healthKnown = ok
						r.ready = hc.Status == "ready"
						r.healthUntil = hc.StaleAfter
					}
					snap.index[tuple(c.Name, o.ID)] = append(snap.index[tuple(c.Name, o.ID)], r)
				}
			}
		}
	}
	nextRetries := map[string]time.Time{}
	for _, k := range snap.known {
		if k.Status.ConsecutiveFailures > 0 {
			nextRetries[k.Status.RetryClass] = earlier(nextRetries[k.Status.RetryClass], k.Status.NextRetryAt)
		}
	}
	for _, class := range []string{"incompatible", "compatible", "unknown", "rejected"} {
		s.rec.SetNextRetry(class, nextRetries[class])
	}
	s.routes.snapshot.Store(snap)
}

// SelectNodes only reads an immutable snapshot and the clock. It never resolves
// an address, reads the persistent cache, fetches health, or waits for a worker.
func (s *Service) SelectNodes(ctx context.Context, f selection.Filter) ([]types.ResolvedNode, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snap := s.routes.snapshot.Load()
	if snap == nil || snap.overlay != s.overlay() {
		return nil, types.ErrRegistryUnavailable
	}
	now := s.clock.Now()
	incomplete := (!s.overlayOnly && (snap.discoveredAt.IsZero() || !now.Before(snap.discoveredAt.Add(discoveryScopeTTL)))) || !snap.complete || now.Before(snap.coverageSince) || (!snap.coverageUntil.IsZero() && !now.Before(snap.coverageUntil))
	candidates := snap.index[tuple(f.Capability, f.Offering)]
	nodes := make([]types.ResolvedNode, 0, len(candidates))
	for _, r := range candidates {
		reason, unknown := routeEligibility(r, now, f)
		if unknown {
			incomplete = true
		}
		if reason != "" {
			if reason == "health_unknown" {
				s.rec.IncLiveHealthDecision(metrics.OutcomeExcludedStale)
			}
			if reason == "health_not_ready" {
				s.rec.IncLiveHealthDecision(metrics.OutcomeExcludedUnhealthy)
			}
			continue
		}
		if s.liveHealth != nil {
			s.rec.IncLiveHealthDecision(metrics.OutcomeAllowedReady)
		}

		nodes = append(nodes, r.node)
	}
	matches := selection.Apply(nodes, f)
	if len(matches) > 0 {
		for i := range matches {
			matches[i] = cloneNode(matches[i])
		}
		return matches, nil
	}
	if incomplete {
		return nil, types.ErrRegistryUnavailable
	}
	return nil, fmt.Errorf("%w: no eligible route for capability=%q offering=%q", types.ErrNotFound, f.Capability, f.Offering)
}

// Discover registers addresses even when their first fetch fails. Replacement
// removes addresses no longer in the chain pool; overlay entries are merged by
// the scheduler. Called by the round seeder without waiting for endpoint I/O.
func (s *Service) Discover(addresses []types.EthAddress) {
	s.routes.mu.Lock()
	defer s.routes.mu.Unlock()
	s.routes.pool = map[types.EthAddress]bool{}
	s.routes.poolSet = true
	for _, a := range addresses {
		s.routes.pool[a] = true
	}
	for a := range s.routes.addresses {
		e, ok := s.overlay().FindByAddress(a)
		if !s.routes.pool[a] && (!ok || !e.Enabled) {
			delete(s.routes.addresses, a)
		}
	}
	for _, a := range addresses {
		st := s.routes.addresses[a]
		// Rediscovery of an unchanged address preserves its cooldown.
		s.routes.addresses[a] = st
	}
	s.routes.discovered = true
	s.routes.discoveredAt = s.clock.Now()
	s.routes.discoveryOK = true
	s.publishLocked()
}

// DiscoveryFailed prevents a missing candidate set from becoming NOT_FOUND.
func (s *Service) DiscoveryFailed() {
	s.routes.mu.Lock()
	defer s.routes.mu.Unlock()
	s.routes.discoveryOK = false
	s.publishLocked()
}

// RunRefresh owns background work and waits for workers on cancellation.
func (s *Service) RunRefresh(ctx context.Context) {
	var wg sync.WaitGroup
	metadataSlots := make(chan struct{}, refreshWorkers)
	healthSlots := make(chan struct{}, refreshWorkers)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	defer wg.Wait()
	for {
		s.schedule(ctx, &wg, metadataSlots, healthSlots)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Service) schedule(ctx context.Context, wg *sync.WaitGroup, meta, health chan struct{}) {
	addrs, err := s.CandidateAddresses()
	s.routes.mu.Lock()
	for a := range s.routes.addresses {
		addrs = append(addrs, a)
	}
	s.routes.mu.Unlock()
	if err == nil {
		for _, a := range addrs {
			if e := s.ensureAddress(ctx, a); e != nil && ctx.Err() == nil {
				s.log.Warn("discovery cache restore failed", "addr", a, "err", e)
			}
		}
	}
	s.routes.mu.Lock()
	if err != nil {
		s.routes.candidateOK = false
	} else {
		s.routes.candidateOK = true
		// Chain discovery establishes completeness separately. Overlay-only has an
		// authoritative local candidate set, including a genuinely empty pool.
		if s.overlayOnly {
			s.routes.discovered = true
			if s.routes.discoveredAt.IsZero() {
				s.routes.discoveredAt = s.clock.Now()
			}
			s.routes.discoveryOK = true
		}
		for _, a := range addrs {
			e, configured := s.overlay().FindByAddress(a)
			if s.routes.poolSet && !s.routes.pool[a] && (!configured || !e.Enabled) {
				continue
			}
			if _, ok := s.routes.addresses[a]; !ok {
				s.routes.addresses[a] = addressState{}
			}
		}
	}
	now := s.clock.Now()
	ov := s.overlay()
	urls := map[string]bool{}
	dueAddresses := make([]types.EthAddress, 0, len(s.routes.addresses))
	for a := range s.routes.addresses {
		dueAddresses = append(dueAddresses, a)
	}
	sort.Slice(dueAddresses, func(i, j int) bool {
		return s.routes.addresses[dueAddresses[i]].next.Before(s.routes.addresses[dueAddresses[j]].next)
	})
	for _, a := range dueAddresses {
		st := s.routes.addresses[a]
		if e, ok := ov.FindByAddress(a); s.routes.poolSet && !s.routes.pool[a] && (!ok || !e.Enabled) {
			delete(s.routes.addresses, a)
			continue
		}
		if s.overlayOnly {
			e, ok := ov.FindByAddress(a)
			if !ok || !e.Enabled {
				delete(s.routes.addresses, a)
				continue
			}
		}
		if st.overlay != ov {
			st = addressState{status: unknownStatus(), overlay: ov}
			s.routes.addresses[a] = st
		}
		key := "address:" + string(a)
		if !s.routes.running[key] && !now.Before(st.next) {
			select {
			case meta <- struct{}{}:
				s.routes.running[key] = true
				wg.Add(1)
				go func(a types.EthAddress, key string) {
					defer wg.Done()
					defer func() { <-meta; s.routes.mu.Lock(); delete(s.routes.running, key); s.routes.mu.Unlock() }()
					if err := s.refreshAddress(ctx, a, false); err != nil && ctx.Err() == nil {
						s.log.Warn("resolver background metadata refresh failed", "addr", a, "err", err)
					}
				}(a, key)
			default:
			}
		}
		if !s.overlayOnly && st.loaded && !st.status.LastAttemptAt.IsZero() && !s.routes.running[key] && !s.routes.running["source:"+string(a)] && !now.Before(st.sourceNext) {
			if e, ok := ov.FindByAddress(a); !ok || e.ManifestURL == "" {
				select {
				case meta <- struct{}{}:
					sourceKey := "source:" + string(a)
					s.routes.running[sourceKey] = true
					wg.Add(1)
					go func(a types.EthAddress, key string) {
						defer wg.Done()
						defer func() { <-meta; s.routes.mu.Lock(); delete(s.routes.running, key); s.routes.mu.Unlock() }()
						s.pollSource(ctx, a)
					}(a, sourceKey)
				default:
				}
			}
		}
		for _, n := range st.nodes {
			urls[n.URL] = true
		}
	}
	dueHealth := make([]string, 0, len(urls))
	for url := range urls {
		dueHealth = append(dueHealth, url)
	}
	sort.Slice(dueHealth, func(i, j int) bool {
		return s.routes.health[dueHealth[i]].next.Before(s.routes.health[dueHealth[j]].next)
	})
	for _, url := range dueHealth {
		if s.liveHealth == nil || s.routes.running["health:"+url] || now.Before(s.routes.health[url].next) {
			continue
		}
		select {
		case health <- struct{}{}:
			wg.Add(1)
			go func(url string) { defer wg.Done(); defer func() { <-health }(); s.RefreshHealth(ctx, url) }(url)
		default:
		}
	}
	s.publishLocked()
	s.routes.mu.Unlock()
}

// cloneNode keeps caller-owned result slices separate from published snapshots.
func cloneNode(n types.ResolvedNode) types.ResolvedNode {
	n.Extra = append([]byte(nil), n.Extra...)
	n.TierAllowed = append([]string(nil), n.TierAllowed...)
	n.SettlementKeys = append([]types.SettlementKey(nil), n.SettlementKeys...)
	if n.Lat != nil {
		x := *n.Lat
		n.Lat = &x
	}
	if n.Lon != nil {
		x := *n.Lon
		n.Lon = &x
	}
	n.Capabilities = append([]types.Capability(nil), n.Capabilities...)
	for i := range n.Capabilities {
		c := &n.Capabilities[i]
		c.Extra = append([]byte(nil), c.Extra...)
		if c.WorkUnitEstimator != nil {
			x := *c.WorkUnitEstimator
			c.WorkUnitEstimator = &x
		}
		c.Offerings = append([]types.Offering(nil), c.Offerings...)
		for j := range c.Offerings {
			c.Offerings[j].Constraints = append([]byte(nil), c.Offerings[j].Constraints...)
		}
	}
	return n
}
