---
title: Resolver cache
status: verified
last-reviewed: 2026-09-24
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
the configured address list. Chain discovery removes departed orchestrators from the selectable in-memory pool;
persistent records and replay watermarks remain for diagnostics and rollback protection.

## Selection snapshots

`Select` and `SelectMany` read one immutable in-memory index keyed by opaque
capability/offering strings (case-insensitive, without semantic rewriting).
They never access the persistent cache, call a provider, or wait for refresh.
Updates publish atomically; each call observes one generation. Equal-weight
routes use deterministic orchestrator-address order and manifest node order.

Before returning a route, selection checks the current time against all required
bounds. Worker delays cannot extend eligibility. When settlement keys are
advertised, at least one must be currently valid; all advertised keys and windows
still travel to the consumer for record-time verification. An absent optional
key list is not synthesized; consumers still enforce their protocol prerequisites.

| State | Hard eligibility bound |
|---|---|
| Signed publication | Earliest of signed expiry, manifest fetch time + manifest TTL, and chain/source observation + chain TTL |
| CSV | Earliest of fetch + manifest TTL and chain observation + chain TTL |
| Static pins / legacy | Source observation + chain TTL; current overlay policy must match |
| Broker tuple health | Broker `stale_after`, capped at five seconds after observation; must be nonzero and `ready` |

Manifest TTL defaults to ten minutes. Internal chain TTL uses `MaxStale`
(default one hour); there is no separate chain-TTL flag. Health never renews
publication or chain timestamps. There is no stale-while-revalidate allowance
past any selection deadline.

## Background refresh

The lifecycle owns a scheduler with four metadata workers and four independent
health workers. Metadata refresh reads chain/source data and verifies manifests;
a stalled chain RPC cannot consume health-worker capacity. Each metadata attempt
and each broker health fetch has a five-second total deadline. A broker response
updates all its tuples in one fetch. Endpoint health requests are deduplicated;
address refreshes serialize with explicit resolution and concurrent background
refresh callers reuse a completed attempt.

Successful refreshes schedule the next attempt at 65–75% of remaining validity,
with deterministic per-endpoint jitter. Failures retry with 1, 2, 4, 8, 16, then
32 second backoff. The scheduler checks due work every 100 ms, serving the oldest due work first. Worker concurrency
and these timing bounds are currently implementation defaults, not CLI flags.
The health HTTP client's `--worker-probe-timeout` can shorten its deadline.

Transport failures preserve only previously verified, still-valid snapshots;
they never renew timestamps. Validation failures revoke the in-memory address
immediately. A changed chain URI revokes the old source even if the replacement
manifest is unreachable. Durable publication watermarks reject rollback and
conflicting same-sequence payloads, including across restart.

Chain round discovery registers the active pool without waiting for each address.
Failed pool enumeration retries every 30 seconds (each enumeration is bounded
by 30 seconds). Overlay-only candidates are scheduled directly without chain I/O.
The registry's multi-RPC client uses a one-second attempt timeout, one retry and
100 ms backoff so a backup can fit within the metadata deadline.

Each completed address publishes independently. Missing, expired or unverified
addresses do not delay a healthy route. Old overlay-generation results cannot
publish into a replacement configuration. Overlays are immutable pointers in the
service API; production loads YAML at startup, so file edits require restart.

## Startup and error semantics

Listeners start while background warming proceeds. Until enough eligible data
exists, selection returns `UNAVAILABLE` with `registry_unavailable`. Persisted
publications retain replay protection. Background warming refetches them; explicit
Resolve may also populate a snapshot from source-matching cached data within
current validity bounds. Health is never restored as fresh from disk.
Explicit dev chain seeds retain their strict startup validation behavior.

A matching eligible route succeeds despite incomplete unrelated state. Without
a match, cold/expired/unknown state returns `UNAVAILABLE`; a complete, fresh
view proving no eligible match returns `NOT_FOUND`. Fresh negative chain lookups
and fresh non-ready health are known negatives; missing/expired health is unknown.
This avoids translating provider failures into apparent route absence.

## Explicit resolution and persistence

`ResolveByAddress` and administrative `Refresh` remain explicit I/O operations.
Resolve preserves its diagnostic inventory behavior, including bounded last-good
fallback and permissive inventory pruning when health is unavailable. Those
results do not relax selection deadlines or signature policy. A force refresh
bypasses its cache TTL; wildcard Refresh still suppresses per-address errors.
Use an address-specific call to diagnose a failure. Selection never calls either
operation or inherits the request's `allow_unsigned` override.

Failed refreshes need not delete persistent records. There is no LRU or automatic
max-stale eviction; retention and selection eligibility are separate concerns.
ListKnown reports cached candidate records, not selectable routes.

## Audit and concurrency

`internal/repo/audit/audit.go` stores gob records in `audit_log`, keyed by
address and RFC3339Nano timestamp. Query scans matching records, with optional
since/limit. There is no automatic 30-day retention. Events at identical
address/timestamp keys can overwrite one another.

Resolver call sites emit `manifest_fetched`, `signature_invalid`, and
`fallback_used` for relevant branches. Other enum values are reserved; their
presence does not mean every cache transition is audited. Canonical payload hashes
and publication sequences enforce a persistent address-scoped replay watermark;
raw body hashes also support migration from old cache records. Repository
acceptance is serialized. Watermark persistence precedes cache replacement,
and signed routes fail closed on persistence errors. See the
[manifest contract](../product-specs/manifest-contract.md) for expiry, restart,
source-change and database-reset semantics.
