package resolver

import (
	"context"
	"sort"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/selection"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

const discoveryScopeTTL = 24 * time.Hour

// KnownAddress includes candidates without a successful publication.
type KnownAddress struct {
	Address  types.EthAddress
	Mode     types.ResolveMode
	CachedAt time.Time
	Until    time.Time
	Status   types.DiscoveryStatus
	HasEntry bool
	Negative bool
}

type CatalogEntry struct {
	Node             types.ResolvedNode
	Selectable       bool
	ExclusionReason  string
	VerifiedAt       time.Time
	ValidUntil       time.Time
	HealthValidUntil time.Time
	Expired          bool
}
type Catalog struct {
	Entries                                                                               []CatalogEntry
	Completeness                                                                          string
	Known, Verified, Incompatible, Unknown, Expired, Deferred, Unavailable                uint32
	SnapshotAt, EvaluatedAt, DiscoveryObservedAt, DiscoveryValidUntil, CoverageValidUntil time.Time
	DiscoveryScope                                                                        string
	ScopeAuthoritative                                                                    bool
}

// routeEligibility is shared by catalog and SelectNodes. The second result says
// whether exclusion means unknown evidence rather than a known policy negative.
func routeEligibility(r indexedRoute, now time.Time, f selection.Filter) (string, bool) {
	if now.Before(r.since) || !now.Before(r.until) {
		return "metadata_expired", true
	}
	if !r.healthKnown || !now.Before(r.healthUntil) {
		return "health_unknown", true
	}
	if !r.ready {
		return "health_not_ready", false
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
			return "settlement_keys_expired", true
		}
	}
	if len(selection.Apply([]types.ResolvedNode{r.node}, f)) == 0 {
		return "policy_filtered", false
	}
	return "", false
}

func (s *Service) KnownAddresses(ctx context.Context) ([]KnownAddress, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snap := s.routes.snapshot.Load()
	if snap == nil || snap.overlay != s.overlay() {
		return nil, nil
	}
	return append([]KnownAddress(nil), snap.known...), nil
}

// ListOfferings performs only an atomic snapshot load and CPU work. Providers,
// persistent storage and refresh coordination are absent from this path.
func (s *Service) ListOfferings(ctx context.Context, f selection.Filter, includeExpired bool) (*Catalog, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := s.clock.Now()
	out := &Catalog{EvaluatedAt: now, Completeness: "uninitialized", DiscoveryScope: "chain_active_pool_and_overlay"}
	if s.overlayOnly {
		out.DiscoveryScope = "overlay_only"
	}
	snap := s.routes.snapshot.Load()
	if snap == nil || snap.overlay != s.overlay() {
		return out, nil
	}
	out.SnapshotAt = snap.at
	out.DiscoveryObservedAt = snap.discoveredAt
	out.ScopeAuthoritative = snap.scopeAuthoritative
	if !s.overlayOnly && !snap.discoveredAt.IsZero() {
		out.DiscoveryValidUntil = snap.discoveredAt.Add(discoveryScopeTTL)
		out.ScopeAuthoritative = out.ScopeAuthoritative && now.Before(out.DiscoveryValidUntil) && !now.Before(snap.discoveredAt)
	}
	incomplete := !snap.complete || !out.ScopeAuthoritative || now.Before(snap.coverageSince) || (!snap.coverageUntil.IsZero() && !now.Before(snap.coverageUntil))
	out.CoverageValidUntil = snap.coverageUntil
	for _, k := range snap.known {
		out.Known++
		switch k.Status.Compatibility {
		case types.CompatibilityVerified:
			out.Verified++
		case types.CompatibilityIncompatible:
			out.Incompatible++
		default:
			out.Unknown++
		}
		if k.HasEntry && !now.Before(k.Until) {
			out.Expired++
		}
		if k.Status.ConsecutiveFailures > 0 && now.Before(k.Status.NextRetryAt) {
			out.Deferred++
		}
		if k.Status.Availability == "unavailable" {
			out.Unavailable++
		}
	}
	keys := make([]tupleKey, 0, len(snap.index))
	for k := range snap.index {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].capability != keys[j].capability {
			return keys[i].capability < keys[j].capability
		}
		return keys[i].offering < keys[j].offering
	})
	for _, key := range keys {
		for _, r := range snap.index[key] {
			_, unknown := routeEligibility(r, now, selection.Filter{})
			if unknown {
				incomplete = true
			}
			out.CoverageValidUntil = earlier(out.CoverageValidUntil, r.healthUntil)
			if !catalogMatches(r.node, f.Capability, f.Offering) {
				continue
			}
			expired := now.Before(r.since) || !now.Before(r.until)
			if expired && !includeExpired {
				continue
			}
			reason, _ := routeEligibility(r, now, f)
			out.Entries = append(out.Entries, CatalogEntry{Node: cloneNode(r.node), Selectable: reason == "", ExclusionReason: reason, Expired: expired, VerifiedAt: r.verifiedAt, ValidUntil: r.until, HealthValidUntil: r.healthUntil})
		}
	}
	if !snap.discoveredAt.IsZero() {
		out.Completeness = "complete"
		if incomplete {
			out.Completeness = "partial"
		}
	}
	return out, nil
}
