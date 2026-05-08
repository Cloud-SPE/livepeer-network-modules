---
title: Manifest contract
stability: v1-stable
last-reviewed: 2026-04-25
---

# Manifest contract

What an operator publishes; what a consumer can rely on.

## What the publisher MUST do

- Host a JSON document at `<serviceURI>/.well-known/livepeer-registry.json`, served over HTTPS (or HTTP if the operator's TLS terminator is upstream).
- The document MUST conform to [docs/design-docs/manifest-schema.md](../design-docs/manifest-schema.md) — schema_version, eth_address, issued_at, nodes, signature.
- The signature MUST be `eth-personal-sign` over the canonical bytes, produced by the same eth key that controls the on-chain `serviceURI`.
- The manifest MUST be ≤ 4 MiB in raw bytes by default. Operators may raise the fetch cap up to 16 MiB via `--manifest-max-bytes`.
- Cache-Control headers SHOULD allow caching (≥ 60s) but MUST NOT exceed the operator's intended update cadence.

## What the publisher MAY do

- Include arbitrary JSON objects under `nodes[].extra`, `capabilities[].extra`, and `offerings[].constraints`.
- Update the manifest at any time without an on-chain transaction.

## What the publisher MUST NOT do

- Sign the manifest with a key other than the one matching the on-chain `eth_address`.
- Encode pricing, geo, or capabilities in the on-chain `serviceURI` itself.
- Serve different manifests based on Origin / User-Agent / IP. Resolvers everywhere SHOULD see the same content.

## What the consumer can rely on

- A successful `Resolver.ResolveByAddress` returns nodes whose `signature_status` is one of: `signed-verified`, `unsigned`, `legacy`. `signed-verified` means the manifest signature recovered to the chain-claimed address.
- `nodes[].url` is a string the consumer can dial directly. The daemon does not transform or rewrite URLs.
- `nodes[].capabilities[].name` is the canonical opaque string per [workload-agnostic-strings.md](../design-docs/workload-agnostic-strings.md). The consumer interprets it.
- `freshness_status` faithfully indicates cache state. A `fresh` result was verified within `--cache-manifest-ttl`.

## What the consumer CANNOT rely on

- That advertised prices in `offerings[].price_per_work_unit_wei` are honored at job time. Pricing is an advertised offer; settlement happens elsewhere (`payment-daemon`).
- That any metadata tucked under `extra` or `constraints` is meaningful to your application. The registry only preserves it; consumers interpret it.
- That `capabilities[]` exhaustively lists everything the orchestrator can do. Operators may withhold capabilities (e.g., for private bilateral arrangements). Use as a discovery hint, not a complete inventory.

## Migration: when an orchestrator moves

If an operator changes their orchestrator URL:

1. Update the manifest at the new URL FIRST.
2. Submit `setServiceURI(new_url)` on chain.
3. The new manifest's signature must be valid for the same eth address (no key rotation across the migration unless they accept downtime).

Resolvers will detect the on-chain change at the next round transition (when the round-anchored cache walks `getServiceURI` for every active orch) or when an operator triggers `Resolver.Refresh()`. On Arbitrum One that's at most ~19 hours; switch operators usually trigger an explicit `Refresh()` to make the change effective immediately.

## Multiple-orchestrator manifests

This is supported but not encouraged. A single eth address publishes one manifest with N nodes. Some operators may run multiple physical orchestrators under one identity for load balancing. Consumers SHOULD treat all nodes in a manifest as equivalent unless `weight` indicates otherwise.

A multi-identity operator (separate eth addresses for different rooms) publishes separate manifests at separate URLs and is not in scope for inter-manifest correlation.
