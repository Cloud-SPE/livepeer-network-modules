---
id: 0010
slug: overlay-manifest-discovery
title: Discover signed manifests from overlay coordinator URLs
status: active
owner: codex
opened: 2026-09-14
---

## Goal

Allow a static orchestrator list to replace chain discovery and serviceURI lookup while preserving the signed-manifest routing and payment contract. Work is tracked in beads `lnm-8ur`.

## Design

An overlay entry may declare `manifest_url`, an absolute HTTPS coordinator manifest URL. This pointer takes precedence over serviceURI and feeds the existing fetch, signature verification, projection and health/selection pipeline. Static `pin` nodes retain their existing explicit unsigned semantics. The expected signer remains `eth_address`; no unsigned or legacy downgrade is allowed for a manifest pointer.

Overlay-only production startup does not construct chain providers. Payment components retain their independent chain configuration. Chain discovery mode still supports explicit overlay URL overrides.

Cache records distinguish overlay pointers from chain pointers so changed configuration cannot reuse a different source. Manifest TTL and forced refresh apply to both sources. Transport outages may serve the same source's verified last-good manifest within max-stale; invalid publications fail closed. Overlay-only selection is restricted to enabled configured entries, excluding old chain cache entries. Selection includes configured overlay addresses so an initially unavailable coordinator is retried on subsequent requests.

## Validation

Exercise strict URL parsing, source precedence with an unusable chain, equivalent signed routes including settlement keys, TTL and forced refresh, cache source changes, signature rejection and bounded outage fallback. Verify production overlay-only provider construction without RPC and startup/selection discovery. Run component tests and lints.

## Decisions

2026-09-14: Use explicit `manifest_url` instead of changing `pin.url`, which already means an unsigned route endpoint. Refresh is demand-driven using the existing resolver TTL; YAML edits require restart. No version bump or image publication is part of this change.

## Validation results

Signed route/settlement parity, price TTL/forced refresh, source changes, invalid publications, bounded outage fallback, production startup without RPC, gRPC selection retry, and old-chain-cache exclusion are covered. Component race tests and Go lint pass. Build and manifest-verification lint pass. The documentation checker has 12 unchanged baseline broken links, tracked in `lnm-qp0`. Coverage-check runs successfully but its enforcement tool is a pre-existing stub; several existing packages are below the documented 75% target.
