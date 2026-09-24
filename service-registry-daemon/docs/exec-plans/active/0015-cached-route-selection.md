---
id: 0015
slug: cached-route-selection
title: Verified snapshot selection and bounded background refresh
status: active
owner: Codex
opened: 2026-09-24
---

## Goal

Remove outbound I/O from Select/SelectMany while preserving signed publication,
health, chain and payment validity. Implements beads `lnm-00f`; the shared RPC
attempt-context correction is tracked by related bead `lnm-ci3k`.

## Approach

A service-owned refresh scheduler maintains independently expiring address and
broker-health records and publishes immutable indexed snapshots. Readers load one
snapshot and enforce hard deadlines at the current time. Refresh starts before
expiry with jitter, bounded concurrency, per-operation deadlines and deduplication.
No reader waits for a refresh or for an unrelated address. Cold or incomplete state
returns unavailable when no eligible result exists; a fresh negative returns not
found. Signature and publication-sequence checks precede publication. Configuration
changes invalidate results from older generations. Persisted publications retain
rollback protection, but health must be fetched anew after restart.

The shared RPC callback accepts the derived attempt context. Parent cancellation
stops the entire retry/failover operation; attempt timeouts permit bounded failover.

## Validation

Use controlled clocks and instrumented providers for cold start, expired state,
invalid signatures, refresh races, worker bounds, failed endpoints and zero-I/O
selection. Exercise actual HTTP RPC cancellation/failover. Run race tests, lints,
coverage gates and a reproducible warm-selection benchmark. A p99 below 100 ms is
the proposed local benchmark target, not a deployment guarantee.

## Decisions log

### 2026-09-24 — Enforce expiry on reads

Refreshing before expiry improves availability, but only read-time checks ensure
worker stalls cannot extend eligibility. Network failure never renews validity.
Work status and dependencies remain in beads.

## Validation evidence

Implementation and documentation are in the working tree. The registry ship-check
passes lint, document checks, the full race suite and the 75% per-package coverage
gate. The chain-commons ship-check and additional deadline/failover regressions
pass. Local benchmark methodology and results are recorded in
[selection performance](../../operations/selection-performance.md). This plan stays
active until the change is merged; beads records implementation completion.
