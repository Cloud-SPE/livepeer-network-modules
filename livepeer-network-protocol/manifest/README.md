# manifest/

JSON Schema for the manifest format orchestrators publish at
`/.well-known/livepeer-registry.json`. Cross-cutting; any change here forces a
spec-wide SemVer bump (per [`../PROCESS.md`](../PROCESS.md)).

**Status:** [`schema.json`](./schema.json) — **draft proposed**, pending user review.

## Files

- [`schema.json`](./schema.json) — the canonical JSON Schema (Draft 2020-12).
- [`examples/`](./examples/) — concrete manifest examples.
  - [`examples/minimal.json`](./examples/minimal.json) — four capabilities (vLLM
    chat, OpenAI-API resale, RTMP video live, a custom `kibble:doggo-bark-counter`)
    showing the workload-agnostic shape.
- [`changelog.md`](./changelog.md) — schema-change history.

## Shape in one paragraph

A manifest is a **two-field outer envelope**: a `manifest` payload + a `signature`
over its JCS-canonicalized form. The payload carries the orch's identity, time
bounds, and a **flat list of capability tuples** — host is not a registration unit.
Each tuple has `capability_id`, `offering_id`, `interaction_mode@vN`, `work_unit.name`,
`price_per_unit_wei` (string-encoded big int), `worker_url` (HTTPS), and optional
free-form `extra` / `constraints` for workload-specific filtering. Signature is
secp256k1 (Ethereum's curve) — recovers to the orch's `eth_address`, which must
match the on-chain `ServiceRegistry` entry.

## Two on-chain registries — one well-known URL

This manifest format covers both transcoding (`ServiceRegistry`) and AI
(`AIServiceRegistry`) workloads in a single unified `capabilities[]` tuple list.
Each tuple's `interaction_mode` field distinguishes the workload class. An orch
may register the same well-known URL in either or both contracts; resolvers
configure which contract address(es) to query for the orch's `serviceURI`.

Livepeer mainnet (Arbitrum One) carries both contracts at distinct addresses:
the legacy `ServiceRegistry` for transcoding workers and the newer
`AIServiceRegistry` for AI workers. Before this rewrite, an operator running
both classes had to publish two different manifests at two different
`serviceURI`s. The rewrite consolidates that: one orch publishes one manifest
at `/.well-known/livepeer-registry.json`, mixes transcoding and AI tuples in
the same `capabilities[]` list, and registers the same URL with whichever
contract addresses the operator participates in. The resolver / gateway side
is configured with which contract address(es) to query for a given orch's
`serviceURI`; the manifest itself is workload-agnostic.

**No `capacity` field** — workers signal saturation via 503 + `Livepeer-Backoff`
(per the headers spec).

## Verification flow

1. Resolver fetches `/.well-known/livepeer-registry.json`.
2. JCS-canonicalize `manifest` payload.
3. Recover signer from `signature.value` (secp256k1).
4. Confirm signer == `manifest.orch.eth_address`.
5. Confirm `eth_address` matches the orch's on-chain `ServiceRegistry` entry.
6. Confirm `now < expires_at`.
7. Confirm `publication_seq > last_seen[eth_address]` (anti-rollback within
   the validity window — resolver caches the last-seen value per
   `eth_address` and rejects equal-or-lower).
8. Index capability tuples for `Resolver.Select(capability_id, offering_id, ...)`.
