---
title: gRPC surface
status: verified
last-reviewed: 2026-04-28
---

# gRPC surface

> **Plan 0043 (decision 8).** The daemon no longer builds or signs
> manifests. `orch-coordinator` builds the candidate and the cold key on
> `secure-orch-console` signs it, so `BuildManifest`, `SignManifest`,
> `BuildAndSign`, `ProbeWorker` and the `livepeer-registry-refresh` CLI
> were deleted along with the daemon's own v3.0.1 manifest schema. What
> remains on `Publisher` is `GetIdentity` and `Health`.

This is the design-side rationale for the gRPC API. The product-side contract (rules consumers can rely on) lives in [docs/product-specs/grpc-surface.md](../product-specs/grpc-surface.md). The actual `.proto` is at `proto-contracts/livepeer/registry/v1/` (sibling module).

## Two services, one binary

| Service | Mounted in mode | Purpose |
|---|---|---|
| `Resolver` | `resolver` | Consumer-side queries: resolve / select / refresh / list |
| `Publisher` | `publisher` | Operator-side actions: build / sign, plus reserved probe stub |

A binary launched in one mode does NOT mount the other service. A consumer accidentally pointed at a publisher daemon gets `Unimplemented`, not a confusing error.

## Resolver methods

| RPC | Purpose |
|---|---|
| `ResolveByAddress(eth_address) → ResolveResult` | Returns the parsed manifest + overlay-merged nodes for one address. The primary call. |
| `Select(filter) → SelectResult` | Gateway-facing route selection across all known addresses. Filters, rank-sorts, then returns one explicit selected route. Stateless w.r.t. previous calls. |
| `ListKnown() → ListKnownResult` | Returns all eth addresses currently in cache, with freshness status. Diagnostic. With `--discovery=chain` the cache is round-seeded; with `--discovery=overlay-only` the cache is overlay-seeded at startup. Either way, `ListKnown` reflects the seeded pool without needing a prior `Refresh` round-trip. |
| `Refresh(eth_address, force) → Empty` | Force a re-fetch. `force=true` ignores TTL. `eth_address="*"` refreshes all. |
| `GetAuditLog(eth_address, since, limit) → AuditLogResult` | Returns audit events. |
| `Health() → HealthResult` | Returns provider health (chain RPC last-success, manifest fetcher latency, cache size). |

## Publisher methods

| RPC | Purpose |
|---|---|
| `GetIdentity() → IdentityResult` | Return the loaded cold-key orch identity for SPA preflight. |
| `Health() → HealthResult` | Same as resolver. |

## Wire types

All eth addresses are `string` (lower-cased 0x-prefixed 40-hex) on the wire — never `bytes`. This is for human-readability in logs and consistency with operator-facing tooling.

Big integers (`price_per_work_unit_wei`) are `string` (decimal). proto's `int64` would alias to JS's number type and lose precision above 2^53.

## Errors

Errors carry a stable `code` string in the status detail (separate from the gRPC status code) so consumers can match on category without parsing English error messages:

| Code | gRPC status | Meaning |
|---|---|---|
| `not_found` | NOT_FOUND | Eth address has no on-chain `serviceURI` |
| `manifest_unavailable` | UNAVAILABLE | Manifest fetch / parse / sig failed and no fallback applied |
| `signature_mismatch` | UNAUTHENTICATED | Manifest signature didn't recover to claimed address |
| `parse_error` | INVALID_ARGUMENT | Manifest body malformed |
| `manifest_too_large` | RESOURCE_EXHAUSTED | Body exceeded `--manifest-max-bytes` |
| `chain_unavailable` | UNAVAILABLE | Chain RPC down beyond TTL |
| `unknown_mode` | INVALID_ARGUMENT | `serviceURI` doesn't match any known mode |
| `cache_stale_failing` | DEADLINE_EXCEEDED | Last-good is too stale and refresh keeps failing |

## No streaming in v1

All RPCs are unary. Streaming subscriptions to "manifest updated" events are listed in `tech-debt-tracker.md` for v2 — it's a real use case, but adds connection-state complexity we don't need yet.

## Auth

Unix-socket only. The local caller is implicitly trusted. There is no auth on the gRPC surface itself. Operators who want network-exposed resolvers must run a TLS reverse proxy and add their own auth — the daemon explicitly does not implement that.
