---
id: 0016
slug: classified-retries-and-catalog
title: Classified persistent retries and cached offering discovery
status: completed
owner: codex
opened: 2026-09-24
---

## Goal

Keep incompatible or unreachable discovery candidates from dominating refresh
work or consumer latency. Give LOC an additive, typed catalog and retry contract.
Implementation is tracked by beads `lnm-j950`; review provenance is `lnm-ll7g`.

## Approach

Persist source-scoped compatibility evidence independently of failures and route
eligibility. Typed HTTP outcomes distinguish missing resources from transient
failure. Only conclusive results across supported manifest locations establish
incompatibility. Validation failures revoke eligibility durably. Existing accepted
publication watermarks continue to prevent rollback across source changes.

Use configurable interval lists for incompatible (5m/30m/2h/6h/24h), previously
verified transient (5s/15s/1m/5m/15m), unknown transient (15s/1m/5m/15m/1h),
and rejected publications (1m/5m/15m/1h). Apply bounded per-attempt jitter and
persist the resulting deadline. Broker health retains its independent policy.
Unchanged rediscovery does not reset retries. Independent bounded source polling
detects URI changes during long manifest cooldowns.

A shared per-address refresh joins concurrent callers. Ordinary reads honor
cooldowns and use eligible cache without network calls; force bypasses scheduling
only. Canceled waiters do not cancel another caller's refresh or count as endpoint
failures. Successful verification and durable acceptance reset backoff.

Add `ListOfferings` to the canonical proto-contracts resolver service. It reads
an immutable snapshot with tuple/provider identities, current selectability,
timestamps, and explicit discovery coverage. Partial and uninitialized catalogs
are successful diagnostic responses, never authoritative absence. Extend
`ListKnown` and resolve results with typed status; deferred errors retain the
stable Struct code and carry typed details plus standard RetryInfo.

## Validation

Race-enabled resolver, repository and gRPC tests cover schedules, restart
persistence, source changes, coalescing, cancellation, ordinary/forced lookups,
signature/expiry/replay rejection, and zero-I/O responsive catalog reads under
stalled providers. Run component build/lint/doc checks and protobuf generation.

## Decisions log

### 2026-09-24 — User authorized implementation

Implement the reviewed LOC proposal additively. Compatibility is evidence about
the current source, not a routing permission. Neither positive nor negative
freshness is extended by delaying a retry. Address-specific diagnostics carry
retry timestamps; aggregate metrics use bounded class labels.

## Artifacts produced

Canonical RPCs: [resolver.proto](../../../../proto-contracts/livepeer/registry/v1/resolver.proto).
Consumer contract: [gRPC surface](../../product-specs/grpc-surface.md).


### 2026-09-24 — LOC contract clarification implemented

Catalog identity is orchestrator/payee plus broker URL and scoped node ID. The
full SelectedRoute projection supplies denominator, estimator, protocol axes,
constraints and settlement metadata; prices are informational. Eligibility uses
the same predicate as SelectMany at evaluated_at. Optional tuple filters never
change unfiltered scope counts. Diagnostic expired inventory is opt-in and always
nonselectable. Canonical protobuf JSON fixtures cover the six requested states.

Validation includes race-enabled daemon/protobuf suites, live gRPC detail and
metadata relay, real Bolt reopen, interrupted persistence, concurrent canceled
waiters, zero-I/O catalog reads under stalled providers, CLI policy validation,
lints, build and the per-package coverage gate. No deployment or image publication
is part of this implementation.
