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

Successful refreshes schedule at 65–75% of remaining validity. Manifest failures
use configurable interval lists, repeated at their final bound:

| Retry class | Default intervals |
|---|---|
| incompatible (missing/unsupported manifest or missing source) | 5m, 30m, 2h, 6h, 24h |
| compatible (previously verified, transient error) | 5s, 15s, 1m, 5m, 15m |
| unknown (unverified, transient error) | 15s, 1m, 5m, 15m, 1h |
| rejected (validation/signature/expiry/replay/internal failure) | 1m, 5m, 15m, 1h |

Each retry uses downward jitter in [80%,100%] of its interval by default,
seeded by address/source/policy step/attempt time; the sampled deadline is
persisted. `--retry-jitter=0` disables jitter. Changing retry class starts that
class at step one while consecutive_failures continues until successful
resolution. Later transport failures at a confirmed-incompatible source retain
the incompatible policy and original evidence deadline; they do not establish new
negative evidence. Broker-health retries independently retain 1,2,4,8,16,32 seconds.
Workers share per-address in-flight refreshes with forced and ordinary callers.
The scheduler checks every 100 ms. Independent source polling (default one minute)
uses the metadata worker budget and checks changed chain URIs during long
manifest cooldowns; unchanged observations never reset the manifest timer.
This is a polling target subject to worker/RPC availability, not a hard SLA.

Transport failures preserve only previously verified, still-valid snapshots;
they never renew timestamps. Validation failures revoke the in-memory address
immediately. A changed chain URI revokes the old source even if the replacement
manifest is unreachable. Durable publication watermarks reject rollback and
conflicting same-sequence payloads, including across restart.

Chain round discovery registers the active pool without waiting for each address
and without clearing retries for unchanged candidates. Scope completeness expires
24 hours after the last successful pool enumeration.
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

`manifest_discovery` stores source identity, compatibility evidence/time bounds,
last manifest availability, failure reason, streak, retry class/policy step, last
verification and the computed retry deadline. It includes never-successful
addresses. Accepted publications remain separately in `manifest_cache`; the
publication watermark remains address-scoped across source changes and deletion.
The resolver restores discovery state before scheduling or explicit resolution.
Old legacy cache entries do not become evidence of manifest incompatibility.

Before an attempt, a durable invalidation guard is written. On completion it is
replaced with accepted state or classified failure. A crash or persistence error
cannot resurrect an old publication after a rejection. Transient failures may
retain previously accepted data only within existing eligibility bounds.
Restart restores accepted inventory but never restores broker health as fresh.

Ordinary Resolve honors cooldown: it returns currently fresh cache without
chain/manifest/health calls or a typed `resolution_deferred`. An attempted refresh
can still return bounded diagnostic last-good after a transport failure; this
never extends selection deadlines. A force request bypasses scheduling, not
validation or source checks. Shared attempts have one five-second total deadline;
individual waiter cancellation neither counts as endpoint failure nor cancels
other waiters. The final waiter cancels and joins the operation.

Source URI changes reset retry eligibility and revoke old-source data before
retrieval. Negative evidence has its own freshness deadline (manifest TTL for
missing/unsupported manifests, source TTL for missing chain pointers); daily
backoff is not daily proof of absence. Successful cache reads never reset counters
or renew evidence. ListKnown and ListOfferings expose immutable diagnostic
snapshots without fetching or waiting. See the [consumer contract](../product-specs/grpc-surface.md).

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
