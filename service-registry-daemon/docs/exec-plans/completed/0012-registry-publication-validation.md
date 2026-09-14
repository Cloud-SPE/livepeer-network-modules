---
id: 0012
slug: registry-publication-validation
title: Enforce publication validity and durable replay protection
status: completed
owner: codex
opened: 2026-09-14
---

## Design

Work is tracked in lnm-dnh. Verify the cold signature before applying the local
clock policy: issued_at <= now < expires_at, with a strictly positive window.
Preserve expiry in the internal projection and recheck every cache read, including
transport/chain fallback. Old cache entries without expiry must be refetched.
Settlement delegations retain their signed windows for consumers validating
settlement timestamps; they are not removed simply because a historical key has
expired at lookup time.

Persist an address-scoped publication high-water mark separately from evictable
cache data. Serialize repository acceptance, rejecting lower sequences and
conflicting payloads at the same sequence. Clarify the protocol verification prose that equal-sequence identical refetches
are idempotent (not new publications). Hash canonical signed payload bytes
so whitespace or signature re-encoding does not create conflicts. Write the
high-water record before caching, and never return new signed routes when durable
acceptance fails. Preserve the high-water mark across source changes, cache
removal and process restarts. A crash between the two writes can deny an older
cache entry but cannot authorize rollback. Existing caches seed the watermark;
without a stored canonical hash, an equal-sequence migration must match the old
raw document hash before adopting the canonical hash. Local database deletion
resets replay history and is an explicit trust reset.

## Validation

Exercise initial fetch, exact expiry, future issue time, inverted windows,
cache and outage expiry, rollback, conflicting same-sequence publications,
identical canonical payload refetch, concurrent acceptance, persistent store
restart, cache deletion, and storage failures. Run full race tests and doc gates.

## Validation result

Completed 2026-09-14. Full ship-check passes (lint, race tests and per-package
coverage). Every executable daemon package exceeds 75%; seeder coverage is 100%.
Generated documentation and current links pass. Both Compose configurations
validate; Prometheus checks all 14 rules and the per-instance idle/failure
regression passes. Historical references and previously completed plans were
preserved. Image publication and Blueclaw environment acceptance are separate
release work, tracked in lnm-yer.
