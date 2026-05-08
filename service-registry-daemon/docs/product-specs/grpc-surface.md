---
title: gRPC surface — product spec
stability: v1-stable
last-reviewed: 2026-04-28
---

# gRPC surface (consumer contract)

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

### Select

Input: `capability`, `offering`, optional `tier`, optional `min_weight`.
Output: one selected route (`worker_url`, `eth_address`, `capability`,
`offering`, `price_per_work_unit_wei`, `work_unit`, optional
`extra_json`, optional `constraints_json`).

Guarantees:
- `capability` and `offering` are required.
- Filtering remains conjunctive across `capability`, `offering`, `tier`,
  and `min_weight`.
- If more than one candidate matches, the daemon applies the existing
  stable weight sort and returns the top-ranked route only.
- The gateway-facing response never includes `worker_eth_address`.

### ListKnown / Refresh / GetAuditLog / Health

Diagnostic. See design-doc for shapes.

`ListKnown` returns whatever the cache currently holds. The cache is seeded automatically: in `--discovery=chain` mode, every round event re-walks the BondingManager pool; in `--discovery=overlay-only` mode, the daemon walks the operator overlay once at startup. Consumers do not need to call `Refresh` before `ListKnown` to see the seeded pool.

## Publisher service

```proto
service Publisher {
  rpc GetIdentity(google.protobuf.Empty) returns (IdentityResult);
  rpc BuildManifest(BuildManifestRequest) returns (BuildResult);
  rpc SignManifest(SignManifestRequest) returns (SignedManifest);
  rpc BuildAndSign(BuildAndSignRequest) returns (SignedManifest);
  rpc ProbeWorker(ProbeWorkerRequest) returns (ProbeResult);
  rpc Health(google.protobuf.Empty) returns (HealthResult);
}
```

### GetIdentity / BuildManifest / SignManifest / BuildAndSign

`GetIdentity` returns the loaded cold-key eth address. It exists so secure-orch UIs can preflight proposal identity before the operator clicks sign.

`BuildManifest` is pure with respect to output bytes, but it validates the proposal first. The request must carry `proposed_eth_address`; if that address does not match the loaded signer identity, the RPC fails.

`SignManifest` reads the loaded keystore key and produces a signed manifest. It also enforces that the manifest's top-level `eth_address` matches the loaded signer identity.

`BuildAndSign` is the one-shot path used by secure-orch tooling. It applies the same identity validation as `BuildManifest`, then signs.

### ProbeWorker

Reserved for future worker HTTP probing. In the current v1
implementation, the method returns `chain_write_failed` /
failed-precondition and does not fetch worker URLs yet.

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
