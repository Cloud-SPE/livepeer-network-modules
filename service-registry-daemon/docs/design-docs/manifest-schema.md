---
title: Manifest schema
status: accepted
last-reviewed: 2026-05-01
---

# Manifest schema

The off-chain JSON document the publisher hosts at the exact URL returned by `getServiceURI()`. The resolver fetches, validates, and verifies it.

## Top-level shape

```jsonc
{
  "schema_version": "3.0.1",                  // required, semver string; validators accept ^3.0.1
  "eth_address": "0xABCD...0123",             // required, 0x-prefixed 40-hex, lowercased
  "issued_at": "2026-04-29T15:00:00Z",        // required, RFC3339 UTC
  "nodes": [ /* see Node */ ],                // required, non-empty
  "signature": {
    "alg": "eth-personal-sign",               // required, exactly this string in v3
    "value": "0x...",                          // required, 65-byte hex (r || s || v)
    "signed_canonical_bytes_sha256": "0x..."  // required, sha256 of the canonical bytes (see signature-scheme.md)
  }
}
```

## Node shape

```jsonc
{
  "id": "node-1",                             // required, operator-chosen unique within manifest
  "url": "https://orch1.example.com:8935",    // required, https URL the consumer dials for work
  "extra": { "geo": { "region": "us-east-1" } }, // optional, opaque JSON object; node-level extension point
  "capabilities": [ /* see Capability */ ]    // required, may be empty array
}
```

`worker_eth_address` is intentionally **not** part of the published
manifest schema in v3.0.1. Workers may surface it on
`/registry/offerings`, and the orch-coordinator may store/display it,
but it is stripped before proposal publication and signing.

## Capability shape

Capabilities are intentionally a **flat list of opaque strings** with optional structured metadata. The registry does not interpret them. See [workload-agnostic-strings.md](workload-agnostic-strings.md) for naming conventions.

```jsonc
{
  "name": "openai:/v1/chat/completions",      // required, opaque string
  "work_unit": "token",                       // optional, opaque string
  "offerings": [                              // optional, may be empty
    {
      "id": "gpt-oss-20b",                    // required when present — opaque tier identifier (model name for AI; preset for video; resolution/fps for streaming)
      "price_per_work_unit_wei": "1000000",   // optional, decimal string (big-int safe). Empty/absent = orch opts that offering out of routing (gateways skip).
      "constraints": { /* arbitrary JSON object */ } // optional, opaque to the daemon
    }
  ],
  "extra": { /* arbitrary JSON object */ }    // optional, opaque to the daemon
}
```

The `extra` blob and `constraints` blob are deliberately schemaless to the registry, but they are not unbounded:
- each must be a JSON object
- max nesting depth is 10
- contents pass through unchanged

**On `offerings[]` naming (v3 rename):** v1 and v2 of this schema called the field `models[]` with a `model` element. Renamed in v3 to `offerings[]` with `id` so the term reads naturally across workload types — for AI workloads `id` is typically the model name (e.g. `gpt-oss-20b`); for video transcoding it's a preset (e.g. `h264-1080p`); for streaming sessions a resolution/fps tier (e.g. `vtuber-1080p30`). The structural shape is unchanged.

## Versioning

`schema_version` is a semver string. This codebase emits `"3.0.1"` and accepts the caret-compatible range `^3.0.1` (`>= 3.0.1`, `< 4.0.0`).

When a resolver receives a `schema_version` it doesn't recognize:
- The resolver rejects it with `invalid_schema_version`.
- It logs the fact so operators can see consumers are running stale binaries.

## Size limits

- Manifest body MUST be ≤ 4 MiB by default after fetch. Larger manifests are rejected with `ErrManifestTooLarge`.
- `--manifest-max-bytes` can raise the cap up to 16 MiB.
- The number of nodes in a single manifest is unbounded by schema, but resolvers may apply per-resolver caps.

## Canonical bytes (for signing)

The signature covers a **canonicalized** representation of the manifest
with two strips applied first:
- `signature`
- `issued_at`

Then the daemon sorts all object keys lexicographically and emits whitespace-free JSON. See [signature-scheme.md](signature-scheme.md) for the exact procedure.

## Validation order (boundary parsing)

The decoder validates in this order, failing fast:

1. JSON well-formedness.
2. Field presence / type via Go struct tags + manual checks.
3. Unknown field rejection (`unknown_field at <path>`).
4. `schema_version` is recognized.
5. `eth_address` is a 0x-prefixed 40-hex string and lower-cased; reject mixed-case (operators must normalize).
6. `nodes` is non-empty.
7. Each node URL is a valid `https://` (or `http://localhost:` for dev) URL.
8. `extra` / `constraints` are JSON objects with max nesting depth 10.
9. `signature.alg` is `eth-personal-sign`.
10. `signature.value` is exactly 65 bytes (130 hex chars + `0x`).
11. Signature recovers to `eth_address` over the canonical bytes.
Any failure produces a `types.ManifestValidationError` with a stable error code; the resolver propagates the code over gRPC so consumers can distinguish `unknown_field`, `invalid_schema_version`, `parse_error`, and `signature_mismatch`.

## Examples

See [docs/generated/manifest-example.md](../generated/manifest-example.md) for a full canonical example produced by `make doc-lint`.
