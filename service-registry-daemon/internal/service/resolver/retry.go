package resolver

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/repo/manifestcache"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/legacy"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

func unknownStatus() types.DiscoveryStatus {
	return types.DiscoveryStatus{Compatibility: types.CompatibilityUnknown, Availability: "unknown", FailureReason: types.FailureNone}
}

func failureReason(err error) types.FailureReason {
	var fe *types.FetchError
	switch {
	case errors.Is(err, types.ErrManifestMissing):
		return types.FailureMissing
	case errors.Is(err, types.ErrManifestUnsupported), errors.Is(err, types.ErrInvalidSchemaVersion), errors.Is(err, types.ErrUnknownMode):
		return types.FailureUnsupported
	case errors.Is(err, types.ErrPublicationReplay):
		return types.FailureReplay
	case errors.Is(err, types.ErrSignatureMismatch), errors.Is(err, types.ErrSignatureMalformed):
		return types.FailureSignature
	case errors.Is(err, types.ErrManifestExpired):
		return types.FailureExpired
	case errors.Is(err, types.ErrChainUnavailable):
		return types.FailureChain
	case errors.Is(err, types.ErrNotFound):
		return types.FailureSourceMissing
	case errors.As(err, &fe):
		return types.FailureHTTP
	case errors.Is(err, types.ErrManifestUnavailable), errors.Is(err, context.DeadlineExceeded):
		return types.FailureTransport
	case errors.Is(err, types.ErrParse), errors.Is(err, types.ErrUnknownField), errors.Is(err, types.ErrManifestTooLarge), errors.Is(err, types.ErrInvalidEthAddress), errors.Is(err, types.ErrInvalidNodeURL), errors.Is(err, types.ErrEmptyNodes):
		return types.FailureInvalid
	default:
		return types.FailureInternal
	}
}
func transientFailure(r types.FailureReason) bool {
	return r == types.FailureTransport || r == types.FailureHTTP || r == types.FailureChain
}
func (s *Service) retrySchedule(st types.DiscoveryStatus) (string, config.Durations) {
	switch st.FailureReason {
	case types.FailureMissing, types.FailureUnsupported, types.FailureSourceMissing:
		return "incompatible", s.retry.Incompatible
	default:
		if !transientFailure(st.FailureReason) {
			return "rejected", s.retry.Rejected
		}
		if st.Compatibility == types.CompatibilityVerified {
			return "compatible", s.retry.Compatible
		}
		if st.Compatibility == types.CompatibilityIncompatible {
			return "incompatible", s.retry.Incompatible
		}
		return "unknown", s.retry.Unknown
	}
}
func (s *Service) retryInterval(addr types.EthAddress, st types.DiscoveryStatus, schedule config.Durations) time.Duration {
	step := int(st.PolicyStep)
	if step < 1 {
		step = 1
	}
	if step > len(schedule) {
		step = len(schedule)
	}
	d := schedule[step-1]
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%s\x00%s\x00%s\x00%d\x00%d", addr, st.SourceURI, st.RetryClass, st.PolicyStep, st.LastAttemptAt.UnixNano())
	// Downward jitter leaves each configured interval (including the daily cap)
	// a hard maximum. Persist the sampled time so restarts never resample it.
	fraction := float64(h.Sum64()%10001) / 10000
	return time.Duration(float64(d) * (1 - s.retry.Jitter*fraction))
}

func (s *Service) state(addr types.EthAddress) addressState {
	s.routes.mu.Lock()
	defer s.routes.mu.Unlock()
	return s.routes.addresses[addr]
}
func (s *Service) entryUntil(e *manifestcache.Entry) time.Time {
	until := e.ChainSeenAt.Add(s.chainTTL)
	if e.Mode == types.ModeWellKnown || e.Mode == types.ModeCSV {
		until = earlier(until, e.FetchedAt.Add(s.manifest))
	}
	if e.Manifest != nil {
		until = earlier(until, e.Manifest.ExpiresAt)
	}
	return until
}

// ensureAddress restores independently persisted retry evidence before either a
// manual lookup or the scheduler can initiate I/O. Catalog reads never call it.
func (s *Service) ensureAddress(ctx context.Context, addr types.EthAddress) error {
	ov := s.overlay()
	if st := s.state(addr); st.loaded && st.overlay == ov {
		return nil
	}
	unlock, err := s.lockAddress(ctx, addr)
	if err != nil {
		return err
	}
	defer unlock()
	if st := s.state(addr); st.loaded && st.overlay == ov {
		return nil
	}
	st := addressState{overlay: ov, status: unknownStatus(), loaded: true}
	saved, ok, err := s.cache.GetDiscovery(addr)
	if err != nil {
		return err
	}
	if ok {
		st.status = saved
		st.next = saved.NextRetryAt
	}
	e, have, err := s.cache.Get(addr)
	if err != nil {
		return err
	}
	overlayEntry, configured := ov.FindByAddress(addr)
	overlayURL := ""
	if configured {
		overlayURL = overlayEntry.ManifestURL
	}
	if saved.OverlayManifestURL != overlayURL || (s.overlayOnly && overlayURL == "" && have && e.Mode != types.ModeStaticOverlay) {
		st.status = unknownStatus()
		st.status.OverlayManifestURL = overlayURL
		st.status.Invalidated = true
		st.next = time.Time{}
		if err := s.cache.PutDiscovery(addr, st.status); err != nil {
			return err
		}
	}
	if have && !st.status.Invalidated && e.OverlayManifestURL == overlayURL && (!st.status.SourceKnown || st.status.SourceURI == e.ResolvedURI) {
		st.entry = e
		st.until = s.entryUntil(e)
		req := Request{Address: addr, metadataOnly: true, overlay: ov}
		res, err := s.buildResultFromEntry(ctx, e, types.Fresh, req)
		if err == nil {
			st.nodes = res.Nodes
			for i := range st.nodes {
				st.nodes[i] = cloneNode(st.nodes[i])
			}
		} else if errors.Is(err, types.ErrManifestExpired) && e.Manifest != nil {
			// Expired, previously accepted publications are diagnostic inventory only.
			st.nodes = applyOverlay(addr, projectManifest(addr, e.Manifest, e.PublicationSeq), ov)
			for i := range st.nodes {
				st.nodes[i] = cloneNode(st.nodes[i])
			}
		} else {
			return err
		}
		if !ok && e.Manifest != nil {
			st.status.Compatibility = types.CompatibilityVerified
			st.status.Availability = "available"
			st.status.SourceURI = e.ResolvedURI
			st.status.SourceKnown = true
			st.status.LastVerifiedAt = e.FetchedAt
			st.status.CompatibilityCheckedAt = e.FetchedAt
			st.status.CompatibilityValidUntil = st.until
		}
	}
	if st.status.Compatibility == types.CompatibilityIncompatible || st.status.FailureReason == types.FailureSourceMissing {
		st.negative = true
		st.until = st.status.CompatibilityValidUntil
	}
	st.sourceNext = st.status.SourceCheckedAt.Add(s.retry.SourcePollInterval)
	s.routes.mu.Lock()
	s.routes.addresses[addr] = st
	s.publishLocked()
	s.routes.mu.Unlock()
	return nil
}

// observeSource executes under the address lock. A changed pointer revokes old
// data before HTTP retrieval and persists that revocation, even if HTTP fails.
func (s *Service) observeSource(req Request, uri string) error {
	now := s.clock.Now()
	st := s.state(req.Address)
	if req.overlay != s.overlay() {
		return nil
	}
	changed := st.status.SourceKnown && st.status.SourceURI != uri
	if changed {
		st.status = unknownStatus()
		st.status.Invalidated = true
		st.entry = nil
		st.nodes = nil
		st.until = time.Time{}
		st.next = time.Time{}
		st.negative = false
	}
	st.status.SourceKnown = true
	st.status.SourceURI = uri
	st.status.SourceCheckedAt = now
	if e, ok := req.overlay.FindByAddress(req.Address); ok {
		st.status.OverlayManifestURL = e.ManifestURL
	}
	st.overlay = req.overlay
	st.sourceNext = now.Add(s.retry.SourcePollInterval)
	// Keep a durable guard throughout a refresh. pollSource restores it on an
	// unchanged observation; an actual new source remains invalidated.
	guard := st.status
	guard.Invalidated = true
	if err := s.cache.PutDiscovery(req.Address, guard); err != nil {
		return err
	}
	s.routes.mu.Lock()
	s.routes.addresses[req.Address] = st
	s.publishLocked()
	s.routes.mu.Unlock()
	return nil
}

type refreshFlight struct {
	done      chan struct{}
	cancel    context.CancelFunc
	waiters   int
	abandoned bool
	err       error
	overlay   *config.Overlay
}

// RefreshAddress is the explicit administrative override. The scheduler calls
// refreshAddress(..., false), which cannot bypass a newly scheduled cooldown.
func (s *Service) RefreshAddress(ctx context.Context, addr types.EthAddress) error {
	return s.refreshAddress(ctx, addr, true)
}
func (s *Service) refreshAddress(ctx context.Context, addr types.EthAddress, force bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.ensureAddress(ctx, addr); err != nil {
		return err
	}
	s.routes.mu.Lock()
	st := s.routes.addresses[addr]
	if !force && s.clock.Now().Before(st.next) {
		s.routes.mu.Unlock()
		return s.resolutionError(addr, types.ErrResolutionDeferred, st.status)
	}
	f := s.routes.flights[addr]
	if f != nil && f.abandoned {
		s.routes.mu.Unlock()
		select {
		case <-f.done:
			return s.refreshAddress(ctx, addr, force)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f == nil {
		// The bounded operation outlives an individual waiter, but is canceled and
		// joined when its last waiter leaves. No background work leaks past shutdown.
		work, cancel := context.WithTimeout(context.WithoutCancel(ctx), refreshTimeout)
		f = &refreshFlight{done: make(chan struct{}), cancel: cancel, overlay: s.overlay()}
		s.routes.flights[addr] = f
		go func() {
			err := s.performRefresh(work, addr, f.overlay)
			cancel()
			s.routes.mu.Lock()
			f.err = err
			delete(s.routes.flights, addr)
			close(f.done)
			s.routes.mu.Unlock()
		}()
	}
	f.waiters++
	s.routes.mu.Unlock()
	select {
	case <-f.done:
	case <-ctx.Done():
	}
	s.routes.mu.Lock()
	f.waiters--
	last := f.waiters == 0
	if last {
		if ctx.Err() != nil {
			f.abandoned = true
		}
		f.cancel()
	}
	s.routes.mu.Unlock()
	if last {
		<-f.done
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.overlay != s.overlay() {
		return nil
	} // stale generation is never published
	return f.err
}
func (s *Service) performRefresh(ctx context.Context, addr types.EthAddress, ov *config.Overlay) error {
	unlock, err := s.lockAddress(ctx, addr)
	if err != nil {
		return err
	}
	defer unlock()
	req := Request{Address: addr, ForceRefresh: true, metadataOnly: true, overlay: ov}
	st := s.state(addr)
	guard := st.status
	guard.Invalidated = true
	if err := s.cache.PutDiscovery(addr, guard); err != nil {
		return err
	}
	_, err = s.resolve(ctx, req)
	if errors.Is(ctx.Err(), context.Canceled) {
		// Local cancellation is not an endpoint observation. Retain any source
		// invalidation already observed, but do not increment failures.
		if restoreErr := s.cache.PutDiscovery(addr, s.state(addr).status); restoreErr != nil {
			return restoreErr
		}
		return ctx.Err()
	}
	return s.acceptAddress(ctx, req, err)
}
func (s *Service) acceptAddress(ctx context.Context, req Request, refreshErr error) error {
	now := s.clock.Now()
	st := s.state(req.Address)
	if req.overlay != s.overlay() {
		return refreshErr
	}
	if refreshErr == nil {
		e, ok, err := s.cache.Get(req.Address)
		if err != nil {
			refreshErr = err
		} else if !ok {
			refreshErr = types.ErrRegistryUnavailable
		} else {
			res, err := s.buildResultFromEntry(ctx, e, types.Fresh, Request{Address: req.Address, metadataOnly: true, overlay: req.overlay})
			if err != nil {
				refreshErr = err
			} else {
				st.entry = e
				st.nodes = res.Nodes
				for i := range st.nodes {
					st.nodes[i] = cloneNode(st.nodes[i])
				}
				st.until = s.entryUntil(e)
				st.negative = false
				st.status.Invalidated = false
				st.status.Availability = "unknown"
				st.status.FailureReason = types.FailureNone
				st.status.ConsecutiveFailures = 0
				st.status.PolicyStep = 0
				st.status.RetryClass = ""
				st.status.LastAttemptAt = now
				if e.Manifest != nil {
					st.status.Availability = "available"
					st.status.Compatibility = types.CompatibilityVerified
					st.status.LastVerifiedAt = now
					st.status.CompatibilityCheckedAt = now
					st.status.CompatibilityValidUntil = st.until
				}
				st.next = refreshAt(now, st.until, string(req.Address))
				st.status.NextRetryAt = st.next
			}
		}
	}
	if refreshErr != nil {
		reason := failureReason(refreshErr)
		previousClass := st.status.RetryClass
		st.status.LastAttemptAt = now
		st.status.Availability = "unavailable"
		st.status.FailureReason = reason
		if st.status.ConsecutiveFailures < ^uint32(0) {
			st.status.ConsecutiveFailures++
		}
		if reason == types.FailureMissing || reason == types.FailureUnsupported {
			st.status.Compatibility = types.CompatibilityIncompatible
			st.status.CompatibilityCheckedAt = now
			st.status.CompatibilityValidUntil = now.Add(s.manifest)
		}
		class, policy := s.retrySchedule(st.status)
		st.status.RetryClass = class
		if class != previousClass {
			st.status.PolicyStep = 1
		} else if st.status.PolicyStep < ^uint32(0) {
			st.status.PolicyStep++
		}
		delay := s.retryInterval(req.Address, st.status, policy)
		s.rec.ObserveDiscoveryRetry(class, string(reason), delay)
		st.next = now.Add(delay)
		st.status.NextRetryAt = st.next
		st.negative = reason == types.FailureMissing || reason == types.FailureUnsupported || reason == types.FailureSourceMissing
		if !transientFailure(reason) {
			st.status.Invalidated = true
			st.entry = nil
			st.nodes = nil
			st.until = time.Time{}
		}
		if st.negative {
			if reason == types.FailureSourceMissing {
				st.status.CompatibilityCheckedAt = now
				st.status.CompatibilityValidUntil = now.Add(s.chainTTL)
			}
			st.until = st.status.CompatibilityValidUntil
		}
	}
	st.loaded = true
	st.overlay = req.overlay
	st.sourceNext = now.Add(s.retry.SourcePollInterval)
	if err := s.cache.PutDiscovery(req.Address, st.status); err != nil {
		// The pre-attempt durable guard prevents resurrection after restart.
		st.entry = nil
		st.nodes = nil
		st.until = time.Time{}
		st.status.Invalidated = true
		refreshErr = err
	}
	s.routes.mu.Lock()
	e, configured := req.overlay.FindByAddress(req.Address)
	if !s.routes.poolSet || s.routes.pool[req.Address] || (configured && e.Enabled) {
		s.routes.addresses[req.Address] = st
		s.publishLocked()
	}
	s.routes.mu.Unlock()
	if refreshErr != nil {
		return s.resolutionError(req.Address, refreshErr, st.status)
	}
	return nil
}
func (s *Service) resolutionError(addr types.EthAddress, cause error, st types.DiscoveryStatus) error {
	now := s.clock.Now()
	after := st.NextRetryAt.Sub(now)
	if after < 0 {
		after = 0
	}
	return &types.ResolutionError{Cause: cause, Address: addr, Status: st, EvaluatedAt: now, RetryAfter: after}
}

func (s *Service) cachedResult(ctx context.Context, req Request, st addressState, cacheOnly bool) (*types.ResolveResult, error) {
	if st.entry == nil || st.status.Invalidated || !s.cacheFresh(st.entry, s.clock.Now()) {
		return nil, types.ErrResolutionDeferred
	}
	req.metadataOnly = cacheOnly
	res, err := s.buildResultFromEntry(ctx, st.entry, types.Fresh, req)
	if res != nil {
		res.DiscoveryStatus = st.status
	}
	return res, err
}
func (s *Service) ResolveByAddress(ctx context.Context, req Request) (result *types.ResolveResult, err error) {
	start := time.Now()
	defer func() {
		if result != nil {
			freshness := metrics.FreshnessFresh
			if result.FreshnessStatus == types.StaleFailing {
				freshness = metrics.FreshnessStaleFailing
			}
			s.rec.IncResolution(modeLabel(result.Mode), freshness)
			s.rec.ObserveResolveDuration(modeLabel(result.Mode), freshness, time.Since(start))
		}
	}()

	req.overlay = s.overlay()
	e, configured := req.overlay.FindByAddress(req.Address)
	if s.overlayOnly && (!configured || !e.Enabled) {
		return nil, types.ErrNotFound
	}
	if err := s.ensureAddress(ctx, req.Address); err != nil {
		return nil, err
	}
	st := s.state(req.Address)
	if !req.ForceRefresh {
		outcome := metrics.CacheMiss
		if st.entry != nil {
			outcome = metrics.CacheHitStale
			if !st.status.Invalidated && s.cacheFresh(st.entry, s.clock.Now()) {
				outcome = metrics.CacheHitFresh
			}
		}
		s.rec.IncCacheLookup(outcome)
		deferred := st.status.ConsecutiveFailures > 0 && s.clock.Now().Before(st.next)
		if res, err := s.cachedResult(ctx, req, st, deferred); err == nil {
			return res, nil
		}
		if deferred {
			s.rec.IncResolutionDeferred()
			return nil, s.resolutionError(req.Address, types.ErrResolutionDeferred, st.status)
		}
	}
	err = s.refreshAddress(ctx, req.Address, true)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	st = s.state(req.Address)
	if req.overlay != s.overlay() {
		return nil, types.ErrRegistryUnavailable
	}
	if err == nil {
		return s.cachedResult(ctx, req, st, false)
	}
	// Existing inventory fallback may expose a still-valid verified publication;
	// it cannot renew cache bounds, reset failures or republish rejected data.
	if transientFailure(st.status.FailureReason) {
		if st.entry != nil && !st.status.Invalidated && s.clock.Now().Sub(st.entry.FetchedAt) < s.maxStale {
			req.metadataOnly = true
			if res, cacheErr := s.buildResultFromEntry(ctx, st.entry, types.StaleFailing, req); cacheErr == nil {
				res.DiscoveryStatus = st.status
				return res, nil
			} else if errors.Is(cacheErr, types.ErrManifestExpired) {
				return nil, s.resolutionError(req.Address, cacheErr, st.status)
			}
		}
		if res, cacheErr := s.cachedResult(ctx, req, st, true); cacheErr == nil {
			return res, nil
		}
	}
	// Legacy fallback remains a diagnostic-only response. It never establishes
	// compatibility, writes accepted metadata or resets the manifest failure state.
	if req.AllowLegacyFallback && (!configured || e.ManifestURL == "") && st.status.SourceURI != "" && (transientFailure(st.status.FailureReason) || st.status.FailureReason == types.FailureMissing) {
		s.rec.IncLegacyFallback(legacyFallbackReason(err))
		s.appendAudit(req.Address, types.AuditFallbackUsed, types.ModeLegacy, "diagnostic legacy fallback: "+err.Error())
		return &types.ResolveResult{EthAddress: req.Address, ResolvedURI: st.status.SourceURI, Mode: types.ModeLegacy, Nodes: []types.ResolvedNode{legacy.Synthesize(req.Address, st.status.SourceURI)}, DiscoveryStatus: st.status}, nil
	}
	return nil, err
}

func (s *Service) pollSource(ctx context.Context, addr types.EthAddress) {
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	unlock, err := s.lockAddress(ctx, addr)
	if err != nil {
		return
	}
	defer unlock()
	req := Request{Address: addr, overlay: s.overlay()}
	st := s.state(addr)
	uri, err := s.chain.GetServiceURI(ctx, addr)
	if err == nil || errors.Is(err, types.ErrNotFound) {
		if err := s.observeSource(req, uri); err != nil {
			s.log.Warn("source observation persistence failed", "addr", addr, "err", err)
			return
		}
		st = s.state(addr)
		if err := s.cache.PutDiscovery(addr, st.status); err != nil {
			s.log.Warn("source state persistence failed", "addr", addr, "err", err)
		}
	}
	st.sourceNext = s.clock.Now().Add(s.retry.SourcePollInterval)
	s.routes.mu.Lock()
	s.routes.addresses[addr] = st
	s.publishLocked()
	s.routes.mu.Unlock()
}

// Match only tuple identity here; policy constraints determine selectable rather
// than hiding inventory that LOC needs to display as unavailable.
func catalogMatches(n types.ResolvedNode, f string, o string) bool {
	c := n.Capabilities[0]
	v := c.Offerings[0]
	return (f == "" || strings.EqualFold(f, c.Name)) && (o == "" || strings.EqualFold(o, v.ID))
}
