# Manifest schema changelog

Schema changes are cross-cutting and force a spec-wide SemVer bump (per
[`PROCESS.md`](../PROCESS.md)).

| Spec version | Date | Change |
|---|---|---|
| 0.1.0 | 2026-05-06 | Initial draft. Two-field outer envelope (`manifest` + `signature`). Manifest payload carries `spec_version`, `issued_at`, `expires_at`, `orch.{eth_address, service_uri?}`, and a flat `capabilities[]` array. Each capability tuple has `capability_id`, `offering_id`, `interaction_mode` (e.g. `http-stream@v1`), `work_unit.name`, `price_per_unit_wei` (string-encoded big int), `worker_url` (HTTPS), optional `extra`, optional `constraints`. No `capacity` field — workers signal saturation via 503 + `Livepeer-Backoff`. Signature is secp256k1 over the JCS-canonicalized (RFC 8785) form of the manifest payload. |
| 0.2.0 | 2026-05-06 | Add `publication_seq` (non-negative integer, required) to the inner manifest payload. Each cold-key signature emits a value strictly greater than every previously-signed value for the same `eth_address`. Resolvers cache the last-seen value per `eth_address` and reject manifests with `publication_seq <= last_seen`, closing the rollback gap inside the `(issued_at, expires_at)` window. Pre-1.0.0 minor bump per `PROCESS.md`. |
