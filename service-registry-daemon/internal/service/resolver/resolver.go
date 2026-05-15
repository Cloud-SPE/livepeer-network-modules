// Package resolver implements the resolver service: turning an
// Ethereum orchestrator address into a list of ResolvedNodes.
//
// The flow per ResolveByAddress:
//
//  1. Look up cache. On hit-with-fresh-TTL, return.
//  2. Read on-chain serviceURI through providers/chain.
//  3. Detect mode (well-known / CSV / unknown).
//  4. Mode-specific decode:
//     - WellKnown: HTTP-fetch /.well-known/...; decode + verify
//     signature against the chain-claimed eth address.
//     - CSV: split + base64-decode; produce unsigned nodes.
//     - WellKnown manifest 404 + allow_legacy_fallback: synthesize
//     a single legacy node from the URL.
//  5. Merge static overlay for policy fields.
//  6. Apply signature policy: drop nodes whose status fails it.
//  7. Cache and return.
//
// All chain/HTTP/I/O lives behind providers/. This file is only
// orchestration.
package resolver

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/livehealthfetcher"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/manifestfetcher"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/verifier"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/repo/manifestcache"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/service/legacy"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

// Service is the resolver business-logic surface used by runtime/grpc.
type Service struct {
	chain     chain.Chain
	fetcher   manifestfetcher.ManifestFetcher
	verifier  verifier.Verifier
	cache     manifestcache.Repo
	audit     audit.Repo
	overlay   func() *config.Overlay // accessor so reload swaps the value atomically
	clock     clock.Clock
	log       logger.Logger
	rec       metrics.Recorder
	chainTTL  time.Duration
	manifest  time.Duration
	maxStale  time.Duration
	rejectUns bool
	liveHealth livehealthfetcher.Fetcher
	liveMu     sync.RWMutex
	liveCache  map[string]liveHealthCacheEntry
}

// Config wires the service.
type Config struct {
	Chain    chain.Chain
	Fetcher  manifestfetcher.ManifestFetcher
	Verifier verifier.Verifier
	Cache    manifestcache.Repo
	Audit    audit.Repo
	Overlay  func() *config.Overlay
	Clock    clock.Clock
	Logger   logger.Logger
	Recorder metrics.Recorder
	// CacheManifestTTL governs how long fetched manifest JSON bodies
	// stay fresh. Pool composition is round-anchored separately by
	// the daemon's seeder loop (see plan 0009 §C).
	CacheManifestTTL time.Duration
	MaxStale         time.Duration
	RejectUnsigned   bool
	LiveHealth       livehealthfetcher.Fetcher
}

// New constructs a resolver Service.
func New(c Config) *Service {
	if c.Clock == nil {
		c.Clock = clock.System{}
	}
	if c.Logger == nil {
		c.Logger = logger.Discard()
	}
	if c.Verifier == nil {
		c.Verifier = verifier.New()
	}
	if c.Overlay == nil {
		empty := config.EmptyOverlay()
		c.Overlay = func() *config.Overlay { return empty }
	}
	if c.Recorder == nil {
		c.Recorder = metrics.NewNoop()
	}
	return &Service{
		chain:    c.Chain,
		fetcher:  c.Fetcher,
		verifier: c.Verifier,
		cache:    c.Cache,
		audit:    c.Audit,
		overlay:  c.Overlay,
		clock:    c.Clock,
		log:      c.Logger,
		rec:      c.Recorder,
		// chainTTL governs the in-cache "is this entry still fresh?"
		// check that the resolver's lookup logic uses. With round-
		// anchored seeding, the seeder overwrites entries on each
		// round event so the TTL effectively never fires for actively-
		// pooled orchs. Hard-coded to MaxStale (the same window as
		// last-good fallback) so a one-shot ResolveByAddress for an
		// address not recently seeded still gets a fresh-enough entry.
		chainTTL:  nonZeroDuration(c.MaxStale, 1*time.Hour),
		manifest:  nonZeroDuration(c.CacheManifestTTL, 10*time.Minute),
		maxStale:  nonZeroDuration(c.MaxStale, 1*time.Hour),
		rejectUns: c.RejectUnsigned,
		liveHealth: c.LiveHealth,
		liveCache:  map[string]liveHealthCacheEntry{},
	}
}

type liveHealthCacheEntry struct {
	status     string
	staleAfter time.Time
	checkedAt  time.Time
}

// Request bundles the inputs for a single resolve.
type Request struct {
	Address             types.EthAddress
	Capability          string
	Offering            string
	AllowLegacyFallback bool
	AllowUnsigned       bool
	ForceRefresh        bool
}

// ResolveByAddress is the primary entrypoint.
func (s *Service) ResolveByAddress(ctx context.Context, req Request) (*types.ResolveResult, error) {
	now := s.clock.Now()
	start := now
	addr := req.Address

	// 1. Cache lookup.
	if !req.ForceRefresh {
		if cached, ok, err := s.cache.Get(addr); err == nil && ok {
			if s.cacheFresh(cached, now) {
				s.rec.IncCacheLookup(metrics.CacheHitFresh)
				res, rerr := s.buildResultFromEntry(ctx, cached, types.Fresh, req)
				if rerr == nil {
					s.rec.IncResolution(modeLabel(cached.Mode), metrics.FreshnessFresh)
					s.rec.ObserveResolveDuration(modeLabel(cached.Mode), metrics.FreshnessFresh, time.Since(start))
				}
				return res, rerr
			}
			s.rec.IncCacheLookup(metrics.CacheHitStale)
			// Stale; refresh inline. (singleflight could be added later.)
		} else {
			s.rec.IncCacheLookup(metrics.CacheMiss)
		}
	}

	// 2. Chain read.
	uri, err := s.chain.GetServiceURI(ctx, addr)
	if err != nil {
		// On chain failure, return last-good if we have one within max-stale.
		if cached, ok, _ := s.cache.Get(addr); ok && now.Sub(cached.FetchedAt) < s.maxStale {
			s.appendAudit(addr, types.AuditFallbackUsed, cached.Mode, "chain unavailable, served last-good: "+err.Error())
			res, rerr := s.buildResultFromEntry(ctx, cached, types.StaleFailing, req)
			if rerr == nil {
				s.rec.IncResolution(modeLabel(cached.Mode), metrics.FreshnessStaleFailing)
				s.rec.ObserveResolveDuration(modeLabel(cached.Mode), metrics.FreshnessStaleFailing, time.Since(start))
			}
			return res, rerr
		}
		if errors.Is(err, types.ErrNotFound) {
			// Static-overlay-only path: an enabled overlay entry with at
			// least one pin can stand in for the chain entirely. Lets
			// operators run resolver against a chainless deployment (the
			// static-overlay-only example).
			if res, ok := s.tryStaticOverlay(addr, now, start, req); ok {
				return res, nil
			}
			return nil, err
		}
		return nil, fmt.Errorf("%w: %w", types.ErrChainUnavailable, err)
	}

	// 3. Mode detection.
	mode := detectMode(uri)
	if mode == types.ModeUnknown {
		return nil, fmt.Errorf("%w: cannot classify serviceURI %q", types.ErrUnknownMode, uri)
	}

	// 4. Mode-specific decode.
	var nodes []types.ResolvedNode
	var manifest *types.Manifest
	var manifestSHA [32]byte
	var legacyURL string

	switch mode {
	case types.ModeWellKnown:
		nodes, manifest, manifestSHA, err = s.fetchAndVerifyManifest(ctx, addr, uri)
		if err != nil {
			// Manifest unreachable; consider legacy fallback.
			if req.AllowLegacyFallback && (errors.Is(err, types.ErrManifestUnavailable) || errors.Is(err, types.ErrManifestTooLarge)) {
				s.rec.IncLegacyFallback(legacyFallbackReason(err))
				s.appendAudit(addr, types.AuditFallbackUsed, types.ModeLegacy, "manifest unavailable, synth legacy: "+err.Error())
				mode = types.ModeLegacy
				legacyURL = uri
				nodes = []types.ResolvedNode{legacy.Synthesize(addr, uri)}
			} else {
				return nil, err
			}
		}
	case types.ModeCSV:
		var defURL string
		defURL, nodes, err = decodeCSV(addr, uri)
		if err != nil {
			return nil, err
		}
		legacyURL = defURL
	case types.ModeUnknown, types.ModeLegacy, types.ModeStaticOverlay:
		// ModeUnknown is rejected above (line ~177); ModeLegacy is set
		// only by the WellKnown branch's fallback path (line ~194);
		// ModeStaticOverlay is set only by tryStaticOverlay (line ~173)
		// which returns directly. None enter this switch fresh. Kept
		// here so the exhaustive linter sees every case explicitly.
	}

	// 5. Overlay merge.
	overlay := s.overlay()
	nodes = applyOverlay(addr, nodes, overlay)
	s.log.Debug("resolver: nodes after overlay",
		"addr", addr,
		"mode", mode,
		"node_count", len(nodes),
		"node_summary", summarizeNodes(nodes),
	)

	// 6. Signature policy.
	allowUns := req.AllowUnsigned || !s.rejectUns
	filtered := nodes[:0]
	for _, n := range nodes {
		if !signaturePolicyAllows(addr, overlay, allowUns, n.SignatureStatus) {
			s.rec.IncOverlayDrop(metrics.OverlayDropSignaturePolicy)
			s.appendAudit(addr, types.AuditFallbackUsed, mode, "dropped node by signature policy: "+n.ID)
			continue
		}
		filtered = append(filtered, n)
	}
	nodes = filtered
	nodes = s.filterLiveHealthy(ctx, nodes, req)
	s.logResolvedResult("resolver: returning fresh result", addr, mode, nodes, types.Fresh)

	// 7. Cache write.
	entry := &manifestcache.Entry{
		EthAddress:     addr,
		ResolvedURI:    uri,
		Mode:           mode,
		Manifest:       manifest,
		LegacyURL:      legacyURL,
		FetchedAt:      now,
		ChainSeenAt:    now,
		ManifestSHA256: manifestSHA,
	}
	if manifest != nil {
		entry.SchemaVersion = manifest.SchemaVersion
	}
	if err := s.cache.Put(entry); err != nil {
		s.log.Warn("cache write failed", "addr", addr, "err", err)
	} else {
		s.rec.IncCacheWrite()
	}

	s.rec.IncResolution(modeLabel(mode), metrics.FreshnessFresh)
	s.rec.ObserveResolveDuration(modeLabel(mode), metrics.FreshnessFresh, time.Since(start))

	return &types.ResolveResult{
		EthAddress:      addr,
		ResolvedURI:     uri,
		Mode:            mode,
		Nodes:           nodes,
		FreshnessStatus: types.Fresh,
		CachedAt:        now,
		FetchedAt:       now,
		Manifest:        manifest,
		SchemaVersion:   entry.SchemaVersion,
	}, nil
}

// fetchAndVerifyManifest fetches the exact on-chain manifest URL and
// validates the signature.
func (s *Service) fetchAndVerifyManifest(ctx context.Context, addr types.EthAddress, manifestURL string) ([]types.ResolvedNode, *types.Manifest, [32]byte, error) {
	if manifestURL == "" {
		return nil, nil, [32]byte{}, fmt.Errorf("%w: empty manifest URL", types.ErrManifestUnavailable)
	}
	candidates := manifestFetchCandidates(manifestURL)
	var lastErr error
	for _, candidateURL := range candidates {
		body, err := s.fetcher.Fetch(ctx, candidateURL)
		if err != nil {
			lastErr = err
			continue
		}

		manifest, canonical, sigHex, err := decodeFetchedManifest(body)
		if err != nil {
			s.rec.IncManifestVerify(metrics.OutcomeParseError)
			s.appendAudit(addr, types.AuditSignatureInvalid, types.ModeWellKnown, "manifest parse: "+err.Error())
			lastErr = err
			// Some deployments still publish a base service URI on chain and
			// expose the signed manifest under /.well-known/..., so keep probing
			// alternate well-known candidates if the direct URL body is not a manifest.
			continue
		}

		claimed, err := types.ParseEthAddress(manifest.EthAddress)
		if err != nil {
			s.rec.IncManifestVerify(metrics.OutcomeParseError)
			lastErr = err
			continue
		}
		if !claimed.Equal(addr) {
			s.rec.IncManifestVerify(metrics.OutcomeEthAddressMismatch)
			lastErr = fmt.Errorf("%w: manifest claims %s, chain says %s", types.ErrSignatureMismatch, claimed, addr)
			continue
		}

		sigBytes, err := decodeSig(sigHex)
		if err != nil {
			s.rec.IncManifestVerify(metrics.OutcomeParseError)
			lastErr = err
			continue
		}
		verifyStart := time.Now()
		recovered, err := s.verifier.Recover(canonical, sigBytes)
		s.rec.ObserveSignatureVerify(time.Since(verifyStart))
		if err != nil {
			s.rec.IncManifestVerify(metrics.OutcomeSignatureMismatch)
			s.appendAudit(addr, types.AuditSignatureInvalid, types.ModeWellKnown, "verify: "+err.Error())
			lastErr = err
			continue
		}
		if !recovered.Equal(addr) {
			s.rec.IncManifestVerify(metrics.OutcomeSignatureMismatch)
			s.appendAudit(addr, types.AuditSignatureInvalid, types.ModeWellKnown, fmt.Sprintf("recovered %s, expected %s", recovered, addr))
			lastErr = fmt.Errorf("%w: recovered %s, expected %s", types.ErrSignatureMismatch, recovered, addr)
			continue
		}
		s.rec.IncManifestVerify(metrics.OutcomeVerified)

		out := projectManifest(addr, manifest)
		s.log.Debug("resolver: manifest projected",
			"addr", addr,
			"schema_version", manifest.SchemaVersion,
			"manifest_url", candidateURL,
			"node_count", len(out),
			"node_summary", summarizeNodes(out),
		)
		sha := bytes32SHA256(body)
		s.appendAudit(addr, types.AuditManifestFetched, types.ModeWellKnown, fmt.Sprintf("nodes=%d schema=%s", len(out), manifest.SchemaVersion))
		return out, manifest, sha, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("%w: no manifest fetch candidates for %q", types.ErrManifestUnavailable, manifestURL)
	}
	return nil, nil, [32]byte{}, lastErr
}

func decodeFetchedManifest(body []byte) (*types.Manifest, []byte, string, error) {
	manifest, err := types.DecodeManifest(body)
	if err == nil {
		canonical, cerr := types.CanonicalBytes(manifest)
		if cerr != nil {
			return nil, nil, "", fmt.Errorf("%w: canonical: %w", types.ErrParse, cerr)
		}
		return manifest, canonical, manifest.Signature.Value, nil
	}

	env, compatErr := types.DecodeCoordinatorEnvelope(body)
	if compatErr != nil {
		return nil, nil, "", err
	}
	canonical, cerr := types.CoordinatorCanonicalBytes(env.Manifest)
	if cerr != nil {
		return nil, nil, "", fmt.Errorf("%w: canonical: %w", types.ErrParse, cerr)
	}
	compatManifest, cerr := env.ToManifest()
	if cerr != nil {
		return nil, nil, "", fmt.Errorf("%w: compatibility projection: %w", types.ErrParse, cerr)
	}
	return compatManifest, canonical, env.Signature.Value, nil
}

// tryStaticOverlay synthesizes a result from the operator overlay alone
// when no chain entry exists. Returns ok=false when the overlay has no
// usable entry for addr — caller falls back to ErrNotFound.
func (s *Service) tryStaticOverlay(addr types.EthAddress, now time.Time, start time.Time, req Request) (*types.ResolveResult, bool) {
	overlay := s.overlay()
	entry, ok := overlay.FindByAddress(addr)
	if !ok || !entry.Enabled || len(entry.Pin) == 0 {
		return nil, false
	}
	nodes := applyOverlay(addr, nil, overlay)
	allowUns := req.AllowUnsigned || !s.rejectUns
	filtered := nodes[:0]
	for _, n := range nodes {
		if !signaturePolicyAllows(addr, overlay, allowUns, n.SignatureStatus) {
			s.rec.IncOverlayDrop(metrics.OverlayDropSignaturePolicy)
			s.appendAudit(addr, types.AuditFallbackUsed, types.ModeStaticOverlay, "dropped node by signature policy: "+n.ID)
			continue
		}
		filtered = append(filtered, n)
	}
	cacheEntry := &manifestcache.Entry{
		EthAddress:  addr,
		Mode:        types.ModeStaticOverlay,
		FetchedAt:   now,
		ChainSeenAt: now,
	}
	if err := s.cache.Put(cacheEntry); err != nil {
		s.log.Warn("cache write failed", "addr", addr, "err", err)
	} else {
		s.rec.IncCacheWrite()
	}
	s.rec.IncResolution(modeLabel(types.ModeStaticOverlay), metrics.FreshnessFresh)
	s.rec.ObserveResolveDuration(modeLabel(types.ModeStaticOverlay), metrics.FreshnessFresh, time.Since(start))
	return &types.ResolveResult{
		EthAddress:      addr,
		Mode:            types.ModeStaticOverlay,
		Nodes:           filtered,
		FreshnessStatus: types.Fresh,
		CachedAt:        now,
		FetchedAt:       now,
	}, true
}

func (s *Service) cacheFresh(e *manifestcache.Entry, now time.Time) bool {
	if e == nil {
		return false
	}
	if e.Mode == types.ModeLegacy {
		// legacy depends only on chain URI; reuse if within chainTTL.
		return now.Sub(e.ChainSeenAt) < s.chainTTL
	}
	if e.Mode == types.ModeStaticOverlay {
		// static-overlay rebuilds nodes from the live overlay accessor on
		// each cache hit, so freshness is bounded by chainTTL only.
		return now.Sub(e.ChainSeenAt) < s.chainTTL
	}
	return now.Sub(e.FetchedAt) < s.manifest && now.Sub(e.ChainSeenAt) < s.chainTTL
}

func (s *Service) buildResultFromEntry(ctx context.Context, e *manifestcache.Entry, freshness types.FreshnessStatus, req Request) (*types.ResolveResult, error) {
	overlay := s.overlay()
	allowUns := req.AllowUnsigned || !s.rejectUns

	var nodes []types.ResolvedNode
	switch e.Mode {
	case types.ModeWellKnown:
		if e.Manifest == nil {
			return nil, fmt.Errorf("%w: cache entry mode=well-known but manifest nil", types.ErrParse)
		}
		nodes = projectManifest(req.Address, e.Manifest)
	case types.ModeCSV:
		// We don't re-decode from cache; CSV-mode entries store nodes... but Entry doesn't carry them.
		// Re-decoding from ResolvedURI is cheap; re-classify and re-decode.
		_, csvNodes, err := decodeCSV(req.Address, e.ResolvedURI)
		if err != nil {
			return nil, err
		}
		nodes = csvNodes
	case types.ModeLegacy:
		nodes = []types.ResolvedNode{legacy.Synthesize(req.Address, e.LegacyURL)}
	case types.ModeStaticOverlay:
		// Cache entry is just a presence marker; rebuild nodes purely
		// from the live overlay (applyOverlay appends pins to nil).
		nodes = nil
	default:
		return nil, fmt.Errorf("%w: cache entry mode=%v", types.ErrUnknownMode, e.Mode)
	}
	s.log.Debug("resolver: cache rebuild before overlay",
		"addr", req.Address,
		"mode", e.Mode,
		"freshness", freshness,
		"node_count", len(nodes),
		"node_summary", summarizeNodes(nodes),
	)

	nodes = applyOverlay(req.Address, nodes, overlay)
	s.log.Debug("resolver: cache rebuild after overlay",
		"addr", req.Address,
		"mode", e.Mode,
		"freshness", freshness,
		"node_count", len(nodes),
		"node_summary", summarizeNodes(nodes),
	)
	filtered := nodes[:0]
	for _, n := range nodes {
		if !signaturePolicyAllows(req.Address, overlay, allowUns, n.SignatureStatus) {
			continue
		}
		filtered = append(filtered, n)
	}
	filtered = s.filterLiveHealthy(ctx, filtered, req)
	s.logResolvedResult("resolver: returning cached result", req.Address, e.Mode, filtered, freshness)
	return &types.ResolveResult{
		EthAddress:      req.Address,
		ResolvedURI:     e.ResolvedURI,
		Mode:            e.Mode,
		Nodes:           filtered,
		FreshnessStatus: freshness,
		CachedAt:        e.FetchedAt,
		FetchedAt:       e.FetchedAt,
		Manifest:        e.Manifest,
		SchemaVersion:   e.SchemaVersion,
	}, nil
}

func projectManifest(addr types.EthAddress, m *types.Manifest) []types.ResolvedNode {
	out := make([]types.ResolvedNode, 0, len(m.Nodes))
	for _, n := range m.Nodes {
		out = append(out, types.ResolvedNode{
			ID:               n.ID,
			URL:              n.URL,
			WorkerEthAddress: n.WorkerEthAddress,
			Extra:            append([]byte(nil), n.Extra...),
			Capabilities:     append([]types.Capability(nil), n.Capabilities...),
			Source:           types.SourceManifest,
			SignatureStatus:  types.SigVerified,
			OperatorAddr:     addr,
			Enabled:          true,
			Weight:           100,
		})
	}
	return out
}

func (s *Service) logResolvedResult(msg string, addr types.EthAddress, mode types.ResolveMode, nodes []types.ResolvedNode, freshness types.FreshnessStatus) {
	kv := []any{
		"addr", addr,
		"mode", mode,
		"freshness", freshness,
		"node_count", len(nodes),
		"node_summary", summarizeNodes(nodes),
	}
	if len(nodes) == 0 {
		s.log.Warn(msg, kv...)
		return
	}
	s.log.Debug(msg, kv...)
}

func (s *Service) filterLiveHealthy(ctx context.Context, nodes []types.ResolvedNode, req Request) []types.ResolvedNode {
	if s.liveHealth == nil {
		return nodes
	}
	if req.Capability != "" && req.Offering != "" {
		out := nodes[:0]
		for _, n := range nodes {
			if !nodeContainsTarget(n, req.Capability, req.Offering) {
				out = append(out, n)
				continue
			}
			status, ok := s.lookupLiveHealth(ctx, n.URL, req.Capability, req.Offering)
			if ok && status == "ready" {
				s.rec.IncLiveHealthDecision(metrics.OutcomeAllowedReady)
				out = append(out, n)
				continue
			}
			if !ok {
				s.rec.IncLiveHealthDecision(metrics.OutcomeExcludedStale)
				continue
			}
			s.rec.IncLiveHealthDecision(metrics.OutcomeExcludedUnhealthy)
		}
		return out
	}
	out := nodes[:0]
	for _, n := range nodes {
		pruned, ok := s.pruneNodeToLiveHealthy(ctx, n)
		if ok {
			out = append(out, pruned)
		}
	}
	return out
}

func (s *Service) pruneNodeToLiveHealthy(ctx context.Context, node types.ResolvedNode) (types.ResolvedNode, bool) {
	if len(node.Capabilities) == 0 {
		return node, true
	}
	snap, err := s.liveHealth.Fetch(ctx, node.URL)
	if err != nil || snap == nil || len(snap.Capabilities) == 0 {
		if err != nil {
			s.rec.IncLiveHealthDecision(metrics.OutcomeLiveHealthFetchErr)
		} else {
			s.rec.IncLiveHealthDecision(metrics.OutcomeLiveHealthMissing)
		}
		return node, true
	}
	healthByTuple := make(map[string]types.RouteHealthCapability, len(snap.Capabilities))
	now := s.clock.Now()
	for _, cap := range snap.Capabilities {
		key := strings.ToLower(cap.ID) + "|" + strings.ToLower(cap.OfferingID)
		healthByTuple[key] = cap
		s.liveMu.Lock()
		s.liveCache[node.URL+"|"+strings.ToLower(cap.ID)+"|"+strings.ToLower(cap.OfferingID)] = liveHealthCacheEntry{
			status:     cap.Status,
			staleAfter: cap.StaleAfter,
			checkedAt:  now,
		}
		s.liveMu.Unlock()
	}
	prunedNode := node
	prunedCaps := make([]types.Capability, 0, len(node.Capabilities))
	for _, cap := range node.Capabilities {
		if len(cap.Offerings) == 0 {
			continue
		}
		prunedCap := cap
		prunedOfferings := make([]types.Offering, 0, len(cap.Offerings))
		for _, off := range cap.Offerings {
			entry, ok := healthByTuple[strings.ToLower(cap.Name)+"|"+strings.ToLower(off.ID)]
			if ok && entry.Status == "ready" && !entry.StaleAfter.IsZero() && now.Before(entry.StaleAfter) {
				s.rec.IncLiveHealthDecision(metrics.OutcomeAllowedReady)
				prunedOfferings = append(prunedOfferings, off)
				continue
			}
			if !ok || entry.StaleAfter.IsZero() || !now.Before(entry.StaleAfter) {
				s.rec.IncLiveHealthDecision(metrics.OutcomeExcludedStale)
				continue
			}
			s.rec.IncLiveHealthDecision(metrics.OutcomeExcludedUnhealthy)
		}
		if len(prunedOfferings) == 0 {
			continue
		}
		prunedCap.Offerings = prunedOfferings
		prunedCaps = append(prunedCaps, prunedCap)
	}
	if len(prunedCaps) == 0 {
		return types.ResolvedNode{}, false
	}
	prunedNode.Capabilities = prunedCaps
	return prunedNode, true
}

func nodeContainsTarget(n types.ResolvedNode, capability, offering string) bool {
	for _, cap := range n.Capabilities {
		if !strings.EqualFold(cap.Name, capability) {
			continue
		}
		for _, off := range cap.Offerings {
			if strings.EqualFold(off.ID, offering) {
				return true
			}
		}
	}
	return false
}

func (s *Service) lookupLiveHealth(ctx context.Context, workerURL, capability, offering string) (string, bool) {
	key := workerURL + "|" + strings.ToLower(capability) + "|" + strings.ToLower(offering)
	now := s.clock.Now()

	s.liveMu.RLock()
	if ent, ok := s.liveCache[key]; ok && !ent.staleAfter.IsZero() && now.Before(ent.staleAfter) {
		s.liveMu.RUnlock()
		return ent.status, true
	}
	s.liveMu.RUnlock()

	snap, err := s.liveHealth.Fetch(ctx, workerURL)
	if err != nil || snap == nil {
		s.liveMu.Lock()
		s.liveCache[key] = liveHealthCacheEntry{status: "stale", checkedAt: now}
		s.liveMu.Unlock()
		return "", false
	}
	for _, cap := range snap.Capabilities {
		if strings.EqualFold(cap.ID, capability) && strings.EqualFold(cap.OfferingID, offering) {
			ent := liveHealthCacheEntry{
				status:     cap.Status,
				staleAfter: cap.StaleAfter,
				checkedAt:  now,
			}
			s.liveMu.Lock()
			s.liveCache[key] = ent
			s.liveMu.Unlock()
			if cap.StaleAfter.IsZero() || now.After(cap.StaleAfter) {
				return "", false
			}
			return cap.Status, true
		}
	}
	s.liveMu.Lock()
	s.liveCache[key] = liveHealthCacheEntry{status: "stale", checkedAt: now}
	s.liveMu.Unlock()
	return "", false
}

func summarizeNodes(nodes []types.ResolvedNode) []map[string]any {
	if len(nodes) == 0 {
		return nil
	}
	const maxNodes = 3
	limit := len(nodes)
	if limit > maxNodes {
		limit = maxNodes
	}
	out := make([]map[string]any, 0, limit)
	for i := 0; i < limit; i++ {
		n := nodes[i]
		out = append(out, map[string]any{
			"id":                 n.ID,
			"url":                n.URL,
			"enabled":            n.Enabled,
			"worker_eth_address": n.WorkerEthAddress,
			"capability_count":   len(n.Capabilities),
			"offering_count":     countOfferings(n.Capabilities),
			"signature_status":   n.SignatureStatus,
		})
	}
	return out
}

func countOfferings(caps []types.Capability) int {
	total := 0
	for _, cap := range caps {
		total += len(cap.Offerings)
	}
	return total
}

func (s *Service) appendAudit(addr types.EthAddress, kind types.AuditKind, mode types.ResolveMode, detail string) {
	if s.audit == nil {
		return
	}
	_ = s.audit.Append(types.AuditEvent{
		At: s.clock.Now(), EthAddress: addr, Kind: kind, Mode: mode, Detail: detail,
	})
}

// decodeSig parses a 0x-prefixed 130-hex signature into 65 bytes.
func decodeSig(s string) ([]byte, error) {
	if !strings.HasPrefix(s, "0x") || len(s) != 132 {
		return nil, fmt.Errorf("%w: expected 0x-prefixed 130-hex", types.ErrSignatureMalformed)
	}
	out := make([]byte, 65)
	for i := 0; i < 65; i++ {
		hi, ok := hexNibble(s[2+i*2])
		lo, ok2 := hexNibble(s[2+i*2+1])
		if !ok || !ok2 {
			return nil, fmt.Errorf("%w: non-hex character", types.ErrSignatureMalformed)
		}
		out[i] = (hi << 4) | lo
	}
	return out, nil
}

func hexNibble(b byte) (byte, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10, true
	default:
		return 0, false
	}
}

// bytes32SHA256 is a thin SHA-256 helper kept locally to avoid pulling
// crypto/sha256 into hot paths that don't need it. Minor duplication
// vs types.CanonicalSHA256, but that returns a hex string.
func bytes32SHA256(b []byte) [32]byte {
	return sha256Sum(b)
}

func nonZeroDuration(d, fallback time.Duration) time.Duration {
	if d <= 0 {
		return fallback
	}
	return d
}

func manifestFetchCandidates(serviceURI string) []string {
	candidates := []string{serviceURI}
	if strings.Contains(serviceURI, "/.well-known/") {
		return candidates
	}

	u, err := url.Parse(serviceURI)
	if err != nil || u.Host == "" {
		return candidates
	}

	base := *u
	base.Path = ""
	base.RawPath = ""
	base.RawQuery = ""
	base.Fragment = ""

	for _, suffix := range []string{
		"/.well-known/livepeer-registry.json",
		"/.well-known/livepeer-ai-registry.json",
	} {
		next := base
		next.Path = suffix
		candidate := next.String()
		if candidate == serviceURI {
			continue
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}
