---
title: Resolver cache
status: verified
last-reviewed: 2026-04-28
---

# Resolver cache

The resolver caches manifests so consumer apps don't pay an HTTP round-trip per request, and so transient manifest-host downtime doesn't immediately break discovery.

## Cache key

`(eth_address)` lower-cased, 0x-prefixed.

The cache key does NOT include the resolved URL — if an operator updates `setServiceURI` on chain, the resolver invalidates the cache entry on the next chain read and re-fetches the manifest from the new URL.

## Storage

`providers/store/chaincommonsadapter` (over `chain-commons.providers.store.bolt`) provides a single `manifest_cache` bucket. Entries are versioned and serialized as Go-gob (no protobuf — internal-only, no need for cross-language stability).

Cache entry shape (`internal/repo/manifestcache/entry.go`):

```go
type Entry struct {
    EthAddress       string                  // lower-cased
    ResolvedURI      string                  // the on-chain serviceURI seen at last fetch (empty for StaticOverlay mode)
    Mode             types.ResolveMode       // WellKnown | CSV | Legacy | StaticOverlay
    Manifest         *types.Manifest         // nil for Legacy / StaticOverlay modes
    LegacyURL        string                  // set for Legacy mode
    FetchedAt        time.Time               // when the manifest was fetched
    ChainSeenAt      time.Time               // when the on-chain serviceURI was last read
    ManifestSHA256   [32]byte                // hash of the raw manifest body (collision-detect for diff alerts)
    SchemaVersion    int                     // mirror of Manifest.SchemaVersion
}
```

## Freshness rules

**Chain reads are round-anchored, not TTL-driven.** The resolver
subscribes to `chain-commons.services.roundclock` and refreshes its
view of the on-chain transcoder pool + `getServiceURI` per address on
every round transition. Pool composition is fixed for the duration of
a round (~19 hours on Arbitrum One), so a fixed TTL would either be
wastefully short or stale; the round event is exactly as fresh as the
underlying data. (See plan 0009 §C, 2026-04-27.) The previously-
documented `--cache-chain-ttl` flag was removed when this change
landed.

Manifest body refreshes still use a TTL:

| TTL | Default | Meaning |
|---|---|---|
| `--cache-manifest-ttl` | 600s (10min) | How long a fetched manifest body is reused before re-fetching from the orch's HTTP server. Independent of chain-side refreshes. |

A `Resolve` request with a cache hit returns immediately with the cached entry. A background manifest re-fetch is kicked off if the body is older than `--cache-manifest-ttl`. Chain re-reads happen only on the next round event (or on operator-triggered `Refresh()`).

`StaticOverlay`-mode entries (see [serviceuri-modes.md](serviceuri-modes.md) §"Mode D") have no manifest and no chain URI — they exist purely as a presence marker so `ListKnown` reflects the seeded pool. On cache hit the resolver rebuilds the node list from the live overlay accessor, so an overlay reload is reflected on the next resolve without invalidating anything. Freshness is bounded by `chainTTL` only.

## Last-good fallback

If a refresh fails (chain RPC down, manifest 503, signature now invalid), the cache entry is **not evicted**. The next `Resolve` continues to return the last-good entry, but the gRPC response includes a `freshness_status` field:

| Status | Meaning |
|---|---|
| `fresh` | Entry was refreshed within TTL. |
| `stale_recoverable` | Entry is past TTL but a background refresh is in flight. |
| `stale_failing` | Last refresh attempt failed; entry is being served from last-good. |

Consumers may opt to short-circuit on `stale_failing` (e.g., a circuit breaker). The default behavior is "trust last-good for `--max-stale` (default 1h)"; past that, the resolver returns `manifest_unavailable`.

## Eviction

Entries are evicted on:
- `setServiceURI` change detected on chain (the URL string differs from `ResolvedURI`).
- `Refresh(eth_address, force=true)` RPC call.
- TTL exceeded for `--max-stale` and refresh continues to fail.

There is no LRU; the cache is bounded by the number of orchestrator addresses the operator queries, which is small (low thousands at the network's current scale).

## Audit log

Every cache transition emits an audit event written to a sibling `audit_log` bucket. Events:

| Event | Trigger |
|---|---|
| `manifest_fetched` | Successful fetch+verify, cache write |
| `manifest_unchanged` | Refresh fetched the same SHA-256 as cached |
| `manifest_changed` | Refresh produced a different SHA-256 (diff details logged) |
| `signature_invalid` | Refresh produced a manifest whose signature didn't recover to the chain-claimed address |
| `chain_uri_changed` | `getServiceURI` returned a value different from `ResolvedURI` |
| `mode_changed` | Resolver mode flipped (e.g., legacy → well-known) |
| `fallback_used` | Last-good was returned instead of fresh |
| `evicted` | Entry was removed |

The audit log is queryable via `Resolver.GetAuditLog(eth_address, since, limit)` for operators debugging discovery issues. Retention: 30 days, then rolling deletion.

## Concurrency

A single `Resolve` for a given eth address coalesces concurrent refreshes via a per-key `singleflight.Group`. If 100 consumers ask for the same orchestrator at once, the resolver issues one chain read + one manifest fetch.

## Crash safety

BoltDB's transactional model gives us crash safety for free. A torn write leaves the previous entry intact. On startup, the resolver does NOT preemptively refresh every cached entry; it lets normal traffic drive refreshes (`--discovery=chain`) or the round-anchored seeder re-walks the pool on the next round event.

An exception: with `--discovery=overlay-only`, the daemon walks every enabled overlay entry once at boot and pre-resolves each address. The cache is warm before the gRPC listener starts accepting traffic, so `ListKnown` and `Select` reflect the operator-curated pool immediately. See [running-the-daemon.md](../operations/running-the-daemon.md) §"Overlay-only seed-on-startup" for the operator flow.

An operator who needs a cold-cache rebuild on chain mode can call `Resolver.Refresh(eth_address="*", force=true)`.
