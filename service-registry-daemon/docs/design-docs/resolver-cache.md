---
title: Resolver cache
status: verified
last-reviewed: 2026-09-14
---

# Resolver cache

## Storage and identity

`internal/repo/manifestcache/cache.go` stores one gob-encoded entry per
lower-cased orchestrator address in `manifest_cache`. The record contains the
resolved URI, configured overlay manifest URL, mode, projected manifest,
publication sequence, schema version string, timestamps and body SHA-256.

`OverlayManifestURL` distinguishes configured pointers from chain-discovered
pointers. Changed/removed configuration cannot reuse a fresh entry from a
different source. In overlay-only mode, old chain cache entries cannot expand
the configured address list. Chain mode does not automatically delete entries
for orchestrators that leave the active pool.

## Freshness and refresh

| Mode | Fresh-cache condition |
|---|---|
| Signed manifest / CSV | Age since fetch below manifest TTL, and age since source check below internal chain TTL |
| Legacy / static pins | Age since source check below internal chain TTL |

Manifest TTL defaults to 10 minutes. Internal chain TTL uses `MaxStale`
(default one hour); there is no separate `--cache-chain-ttl` flag.
`ForceRefresh` bypasses freshness checks.

A stale request performs resolution synchronously, including chain lookup when
applicable and HTTP fetch. There is no stale-while-revalidate background task
or per-address singleflight. Concurrent misses can fetch the same source more
than once.

Chain mode separately subscribes to round events, enumerates active
orchestrators and force-refreshes them. Overlay-only warms enabled entries
once before readiness and then refreshes on demand. Select/SelectMany and
wildcard Refresh include configured pointers even if startup fetching failed.
ListKnown is a diagnostic view of cached candidate addresses, not a refresh.

Static-pin entries are presence markers. Each read rebuilds pins from the
in-memory overlay. The file is loaded only at startup; edit it and restart.

## Failure behavior

- A chain lookup failure can serve a source-matching last-good entry while
  its fetch age is below max-stale.
- Manifest transport failure can serve a verified entry for the same URI and
  source while its fetch age is below max-stale.
- Invalid schema/signature responses and oversized manifest bodies do not use
  the manifest-outage last-good path.
- Explicit overlay manifest pointers never downgrade to legacy routes.
- Chain URLs may use legacy synthesis when requested and fetching is
  unavailable or too large. That node has no capabilities or settlement keys.

Successful cache reads report `fresh`. Last-good fallback reports
`stale_failing`. `stale_recoverable` is a wire enum but is not emitted by the
current resolver. Past max-stale, the underlying failure is returned; the
resolver does not specifically emit `cache_stale_failing` in that path.

Failed refreshes leave the cache record intact. URL changes overwrite it after
successful resolution. Forced Refresh does not delete it. There is no LRU,
automatic max-stale eviction, or periodic cache cleanup. Max-stale bounds reuse
on failure; it is not a retention policy or manifest-expiry check.

## Audit and concurrency

`internal/repo/audit/audit.go` stores gob records in `audit_log`, keyed by
address and RFC3339Nano timestamp. Query scans matching records, with optional
since/limit. There is no automatic 30-day retention. Events at identical
address/timestamp keys can overwrite one another.

Resolver call sites emit `manifest_fetched`, `signature_invalid`, and
`fallback_used` for relevant branches. Other enum values are reserved; their
presence does not mean every cache transition is audited. The stored body hash
is not currently used to enforce publication monotonicity or generate changed/
unchanged events. See [manifest validation limits](../product-specs/manifest-contract.md).

BoltDB transactions protect individual store writes. This does not make
concurrent fetches ordered by publication sequence; replay protection is
tracked separately in `lnm-dnh`.
