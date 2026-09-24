package resolver

import (
	"context"
	"errors"
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
	failures    int
	negative    bool
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
	healthUntil        time.Time
	healthKnown, ready bool
}
type routeSnapshot struct {
	overlay       *config.Overlay
	index         map[tupleKey][]indexedRoute
	coverageUntil time.Time
	coverageSince time.Time
	complete      bool
}
type routeState struct {
	mu          sync.Mutex
	addresses   map[types.EthAddress]addressState
	health      map[string]healthState
	locks       map[types.EthAddress]chan struct{}
	running     map[string]bool
	snapshot    atomic.Pointer[routeSnapshot]
	discovered  bool
	discoveryOK bool
	candidateOK bool
	pool        map[types.EthAddress]bool
	poolSet     bool
	versions    map[types.EthAddress]uint64
	results     map[types.EthAddress]error
}

func newRouteState() *routeState {
	return &routeState{addresses: map[types.EthAddress]addressState{}, health: map[string]healthState{}, locks: map[types.EthAddress]chan struct{}{}, running: map[string]bool{}, candidateOK: true, versions: map[types.EthAddress]uint64{}, results: map[types.EthAddress]error{}}
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

// ResolveByAddress is an explicit I/O operation. Select never invokes it.
// Address serialization also protects publication order against manual refreshes.
func (s *Service) ResolveByAddress(ctx context.Context, req Request) (*types.ResolveResult, error) {
	unlock, err := s.lockAddress(ctx, req.Address)
	if err != nil {
		return nil, err
	}
	defer unlock()
	req.overlay = s.overlay()
	s.routes.mu.Lock()
	quarantined := s.routes.addresses[req.Address].failures > 0
	s.routes.mu.Unlock()
	if quarantined {
		req.ForceRefresh = true
		req.strict = true
	}
	res, err := s.resolve(ctx, req)
	s.acceptAddress(ctx, req, err)
	return res, err
}

// RefreshAddress performs bounded metadata-only refresh; health has its own workers.
func (s *Service) RefreshAddress(ctx context.Context, addr types.EthAddress) error {
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	s.routes.mu.Lock()
	version := s.routes.versions[addr]
	s.routes.mu.Unlock()
	unlock, err := s.lockAddress(ctx, addr)
	if err != nil {
		return err
	}
	defer unlock()
	s.routes.mu.Lock()
	changed := s.routes.versions[addr] != version
	lastErr := s.routes.results[addr]
	s.routes.mu.Unlock()
	if changed {
		return lastErr
	}
	req := Request{Address: addr, ForceRefresh: true, metadataOnly: true, strict: true, overlay: s.overlay()}
	_, err = s.resolve(ctx, req)
	s.acceptAddress(ctx, req, err)
	return err
}

func (s *Service) acceptAddress(ctx context.Context, req Request, refreshErr error) {
	now := s.clock.Now()
	var e *manifestcache.Entry
	var nodes []types.ResolvedNode
	if refreshErr == nil {
		var ok bool
		e, ok, refreshErr = s.cache.Get(req.Address)
		if refreshErr == nil && !ok {
			refreshErr = types.ErrRegistryUnavailable
		}
		if refreshErr == nil {
			rawReq := req
			rawReq.metadataOnly = true
			rawReq.AllowUnsigned = false
			var res *types.ResolveResult
			res, refreshErr = s.buildResultFromEntry(ctx, e, types.Fresh, rawReq)
			if refreshErr == nil {
				nodes = res.Nodes
				for i := range nodes {
					nodes[i] = cloneNode(nodes[i])
				}
			}
		}
	}
	s.routes.mu.Lock()
	defer s.routes.mu.Unlock()
	s.routes.versions[req.Address]++
	s.routes.results[req.Address] = refreshErr
	if req.overlay != s.overlay() {
		return
	}
	overlayEntry, configured := req.overlay.FindByAddress(req.Address)
	if s.routes.poolSet && !s.routes.pool[req.Address] && (!configured || !overlayEntry.Enabled) {
		return
	}
	old := s.routes.addresses[req.Address]
	if refreshErr != nil {
		if errors.Is(refreshErr, types.ErrNotFound) {
			until := now.Add(s.chainTTL)
			s.routes.addresses[req.Address] = addressState{negative: true, overlay: req.overlay, until: until, next: refreshAt(now, until, string(req.Address))}
			s.publishLocked()
			return
		}
		old.failures++
		old.negative = false
		old.next = now.Add(retryDelay(old.failures))
		// Only transport failures may retain still-valid data. Validation failures
		// revoke the in-memory route immediately, without deleting durable watermarks.
		if !errors.Is(refreshErr, types.ErrChainUnavailable) && !errors.Is(refreshErr, types.ErrManifestUnavailable) && !errors.Is(refreshErr, context.DeadlineExceeded) && !errors.Is(refreshErr, context.Canceled) {
			old.entry = nil
			old.nodes = nil
			old.until = time.Time{}
		}
		old.overlay = req.overlay
		s.routes.addresses[req.Address] = old
		s.publishLocked()
		return
	}
	until := e.ChainSeenAt.Add(s.chainTTL)
	if e.Mode == types.ModeWellKnown || e.Mode == types.ModeCSV {
		until = earlier(until, e.FetchedAt.Add(s.manifest))
	}
	if e.Manifest != nil {
		until = earlier(until, e.Manifest.ExpiresAt)
	}
	s.routes.addresses[req.Address] = addressState{entry: e, nodes: nodes, overlay: req.overlay, until: until, next: refreshAt(now, until, string(req.Address))}
	s.publishLocked()
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
	snap := &routeSnapshot{overlay: ov, index: map[tupleKey][]indexedRoute{}, complete: s.routes.discovered && s.routes.discoveryOK && s.routes.candidateOK}
	addrs := make([]string, 0, len(s.routes.addresses))
	for a := range s.routes.addresses {
		addrs = append(addrs, string(a))
	}
	sort.Strings(addrs)
	for _, a := range addrs {
		st := s.routes.addresses[types.EthAddress(a)]
		if st.overlay != ov || (st.entry == nil && !st.negative) {
			snap.complete = false
			continue
		}
		snap.coverageUntil = earlier(snap.coverageUntil, st.until)
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
					r := indexedRoute{node: scoped, until: st.until, healthKnown: s.liveHealth == nil, ready: s.liveHealth == nil, healthUntil: st.until}
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
	incomplete := !snap.complete || now.Before(snap.coverageSince) || (!snap.coverageUntil.IsZero() && !now.Before(snap.coverageUntil))
	candidates := snap.index[tuple(f.Capability, f.Offering)]
	nodes := make([]types.ResolvedNode, 0, len(candidates))
	for _, r := range candidates {
		if now.Before(r.since) || !now.Before(r.until) {
			incomplete = true
			continue
		}
		if !r.healthKnown || !now.Before(r.healthUntil) {
			s.rec.IncLiveHealthDecision(metrics.OutcomeExcludedStale)
			incomplete = true
			continue
		}
		if !r.ready {
			s.rec.IncLiveHealthDecision(metrics.OutcomeExcludedUnhealthy)
			continue
		}
		if len(r.node.SettlementKeys) > 0 {
			valid := false
			for _, k := range r.node.SettlementKeys {
				if !now.Before(k.NotBefore) && now.Before(k.ExpiresAt) {
					valid = true
					break
				}
			}
			if !valid {
				incomplete = true
				continue
			}
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

// invalidateSource promptly revokes a route when chain data points elsewhere,
// even if fetching the new manifest subsequently fails.
func (s *Service) invalidateSource(req Request, uri string) {
	s.routes.mu.Lock()
	defer s.routes.mu.Unlock()
	st := s.routes.addresses[req.Address]
	if st.entry != nil && st.entry.ResolvedURI != uri && req.overlay == s.overlay() {
		st.entry = nil
		st.nodes = nil
		st.until = time.Time{}
		s.routes.addresses[req.Address] = st
		s.publishLocked()
	}
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
		st.next = time.Time{}
		s.routes.addresses[a] = st
	}
	s.routes.discovered = true
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
	if err != nil {
		s.routes.candidateOK = false
	} else {
		s.routes.candidateOK = true
		// Chain discovery establishes completeness separately. Overlay-only has an
		// authoritative local candidate set, including a genuinely empty pool.
		if s.overlayOnly {
			s.routes.discovered = true
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
		if s.overlayOnly {
			e, ok := ov.FindByAddress(a)
			if !ok || !e.Enabled {
				delete(s.routes.addresses, a)
				continue
			}
		}
		if st.overlay != ov {
			st = addressState{}
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
					if err := s.RefreshAddress(ctx, a); err != nil && ctx.Err() == nil {
						s.log.Warn("resolver background metadata refresh failed", "addr", a, "err", err)
					}
				}(a, key)
			default:
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
