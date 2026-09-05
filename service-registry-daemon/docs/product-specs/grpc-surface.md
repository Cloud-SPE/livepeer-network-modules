---
title: gRPC surface — product spec
stability: v1-stable
last-reviewed: 2026-05-19
---

# gRPC surface (consumer contract)

> **Plan 0043 (decision 8).** The daemon no longer builds or signs
> manifests. `orch-coordinator` builds the candidate and the cold key on
> `secure-orch-console` signs it, so `BuildManifest`, `SignManifest`,
> `BuildAndSign`, `ProbeWorker` and the `livepeer-registry-refresh` CLI
> were deleted along with the daemon's own v3.0.1 manifest schema. What
> remains on `Publisher` is `GetIdentity` and `Health`.

This is what a consumer can rely on across versions of the daemon. It's deliberately narrower than the design-doc version: design can change, contract cannot.

## Stability rules

- **v1 services** (`livepeer.registry.v1.Resolver`, `livepeer.registry.v1.Publisher`) are stable. Method signatures, field numbers, and error codes will not change.
- New optional fields may be added to existing messages without bumping the version.
- New methods may be added to existing services without bumping the version.
- Removing methods or required fields requires a `v2` package.
- Error codes (the `code` string in status detail) are stable and never reused for a different meaning.

## Resolver service

```proto
service Resolver {
  rpc ResolveByAddress(ResolveByAddressRequest) returns (ResolveResult);
  rpc Select(SelectRequest) returns (SelectResult);
  rpc SelectMany(SelectRequest) returns (SelectManyResult);
  rpc ListKnown(ListKnownRequest) returns (ListKnownResult);
  rpc Refresh(RefreshRequest) returns (google.protobuf.Empty);
  rpc GetAuditLog(GetAuditLogRequest) returns (AuditLogResult);
  rpc Health(google.protobuf.Empty) returns (HealthResult);
}
```

### ResolveByAddress

Input: `eth_address` (string), `allow_legacy_fallback` (bool), `allow_unsigned` (bool).
Output: `mode`, `nodes[]`, `freshness_status`, `cached_at`.

Guarantees:
- If the on-chain `serviceURI` exists and is dial-able as a URL, AND `allow_legacy_fallback=true`, this RPC NEVER returns `not_found` — at minimum a single legacy-synthesized node is returned.
- `nodes[]` is non-empty on success.
- `freshness_status` is one of `fresh`, `stale_recoverable`, `stale_failing`. Consumers can choose to short-circuit on the latter.
- `nodes[].capabilities[].name` and `nodes[].capabilities[].offerings[].id`
  are discovery metadata, not normalized gateway aliases. Consumers must
  treat capability IDs as opaque strings when feeding a discovered tuple
  back into `Select` / `SelectMany`.

### Select

Input: `capability`, `offering`, optional `tier`, optional `min_weight`.
Output: one selected route (`worker_url`, `eth_address`, `capability`,
`offering`, `price_per_work_unit_wei`, `work_unit`, `units_per_price`,
`quote_id`, `quote_version`, `constraint_fingerprint`,
`route_fingerprint`, optional `extra_json`, optional `constraints_json`).

Guarantees:
- `capability` and `offering` are required.
- `capability` and `offering` are matched case-insensitively but are
  otherwise exact keys. The resolver does not normalize slash-form and
  colon-form OpenAI capability IDs into a shared canonical form. If
  discovery returned `openai:/v1/chat/completions`, callers must use
  that exact capability string in `Select` / `SelectMany`; likewise for
  `openai:chat-completions`.
- Filtering remains conjunctive across `capability`, `offering`, `tier`,
  and `min_weight`.
- A single node may publish multiple entries with the same capability
  name and different offerings. Selection matches across all
  capability/offering tuples on the node; consumers must not assume
  capability names are unique within `nodes[].capabilities[]`.
- If more than one candidate matches, the daemon applies the existing
  stable weight sort and returns the top-ranked route only.
- The gateway-facing response never includes `worker_eth_address`.
- `units_per_price` is always `1` on the current manifest-backed route
  surface because the signed manifest already publishes normalized
  per-work-unit pricing.
- `quote_version` is the signed manifest `publication_seq` when the
  selected route came from the orch-coordinator envelope; otherwise `0`.
- `constraint_fingerprint` is the SHA-256 of canonicalized
  `constraints_json`. Absent or empty constraints canonicalize to `{}`
  before hashing, so `constraint_fingerprint` is always non-empty for a
  selectable route. A populated constraint block always re-marshals
  through JSON canonicalization and therefore cannot collide with the
  empty-object digest.
- `route_fingerprint` is the SHA-256 of the selected route record
  (`eth_address`, `worker_url`, `capability`, `offering`,
  `price_per_work_unit_wei`, `work_unit`, `units_per_price`, canonical
  `extra_json`, canonical `constraints_json`).
- `quote_id` is a deterministic resolver-generated identity for the
  selected route based on `eth_address`, `worker_url`, `capability`,
  `offering`, and `work_unit`.

### SelectMany

Input: identical to `Select`.
Output: ordered `routes[]`, where each element has the same shape as
`Select.route`.

Guarantees:
- `routes[]` is sorted in the same resolver order `Select` uses.
- `Select.route` is always `SelectMany.routes[0]` for the same request.
- Every returned route is payment-ready and carries the full quote-bound
  metadata needed for `CreatePayment`.
- Request-scoped route readiness is derived from the worker's
  `/registry/health` response when available. That health payload is an
  additive surface: brokers may add new fields over time, and resolver
  implementations must continue decoding the fields needed for route
  selection (`id`, `offering_id`, `status`, `stale_after`) rather than
  rejecting the response because of unrelated extra fields.
- `ResolveByAddress` inventory tuples are only selectable when callers
  pass the same capability/offering keys back into `SelectMany`. Route
  visibility in discovery does not override live-health, tier, or
  `min_weight` pruning.

### ListKnown / Refresh / GetAuditLog / Health

Diagnostic. See design-doc for shapes.

`ListKnown` returns whatever the cache currently holds. The cache is seeded automatically: in `--discovery=chain` mode, every round event re-walks the BondingManager pool; in `--discovery=overlay-only` mode, the daemon walks the operator overlay once at startup. Consumers do not need to call `Refresh` before `ListKnown` to see the seeded pool.

## Publisher service

```proto
service Publisher {
  rpc GetIdentity(google.protobuf.Empty) returns (IdentityResult);
  rpc Health(google.protobuf.Empty) returns (HealthResult);
}
```

### GetIdentity

`GetIdentity` returns the loaded cold-key eth address. It exists so
secure-orch UIs can preflight proposal identity before the operator
clicks sign. It is the only publisher-side RPC that survives plan 0043
besides `Health` — building and signing moved to `orch-coordinator` and
`secure-orch-console`.

## Error codes (frozen)

| Code | Meaning |
|---|---|
| `not_found` | Eth address has no on-chain `serviceURI` |
| `manifest_unavailable` | Manifest fetch / parse / sig failed and no fallback applied |
| `signature_mismatch` | Manifest signature didn't recover to claimed address |
| `parse_error` | Manifest body malformed |
| `manifest_too_large` | Body exceeded `--manifest-max-bytes` |
| `chain_unavailable` | Chain RPC down beyond TTL |
| `unknown_mode` | `serviceURI` doesn't match any known mode |
| `cache_stale_failing` | Last-good is too stale and refresh keeps failing |
| `keystore_locked` | Publisher needs a keystore but none was loaded |
| `chain_write_failed` | reserved publisher-side probe path failed |

These strings will not be reused for different meanings. New codes may be added.

## Versioning posture

The proto package is `livepeer.registry.v1`. A future `v2` package will live alongside, and the daemon will mount both for the migration window. We commit to a minimum 12-month overlap before removing v1.
