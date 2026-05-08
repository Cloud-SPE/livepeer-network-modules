# Review of the on-chain CSV proposal

A 2026-Q1 proposal suggested encoding richer metadata on chain by overloading `ServiceRegistry.serviceURI` as a CSV: `<defaultURI>,<version>,<base64_json>`. This document captures why we rejected it and how the current architecture (off-chain manifest at a well-known path) addresses the same goals more cleanly.

## What the proposal asked for

- Multi-node advertisement under one orchestrator identity
- Geo / capability / GPU metadata
- Off-chain capability updates without an on-chain transaction
- Backwards compatibility with old `getServiceURI` clients

## Why we rejected the on-chain CSV

1. **CSV ambiguity.** URLs may legitimately contain commas (RFC 3986 path/query). `strings.SplitN(",",3)` works for the proposed shape but breaks if anyone writes a URL with embedded commas. No clean way to escape.
2. **Base64 inflates on-chain bytes.** ~33% size blowup, paid every time the orchestrator updates metadata. Solidity `string` storage is per-byte. The proposal's "minimal gas impact" claim isn't quantified.
3. **Wrong layer.** Off-chain HTTP already served capabilities just fine in the bridge/worker pair we observed. Putting structured data on chain for off-chain reads is layered backwards — the chain should be the trust anchor, not the data carrier.
4. **No consumer.** The proposal named a "Patch Point" in `discovery/eth_discovery.go` that doesn't exist in `go-livepeer`. No code path currently reads `serviceURI` and routes by capability — it's used only to dial.
5. **Trust model unclear.** Proposal mentioned signing the JSON but didn't specify how. Meanwhile the gRPC `OrchestratorInfo` path is already authenticated by the orchestrator's PM signing key.
6. **Schema collision risk.** The bridge already had a static-overlay format; the worker already had a `/capabilities` JSON; go-livepeer had its own `Capabilities` proto. Inventing a fourth format would fragment further.

## What we built instead

- The on-chain `serviceURI` stays a **plain URL**. Old transcoding clients dial it as before. No CSV parsing risk.
- The publisher hosts a signed manifest at a well-known sub-path (`/.well-known/livepeer-registry.json`) of that URL.
- The resolver fetches, verifies, caches, and exposes the parsed manifest over gRPC.
- The manifest reuses the worker's existing `/capabilities` JSON shape so worker-node needs zero changes.
- A static `nodes.yaml` overlay preserves the bridge's existing operator-curated posture.
- Capability strings are opaque to the daemon — no enum, no parsing, no domain coupling.

## Same goals, addressed

| Proposal goal | Our approach |
|---|---|
| Multi-node under one identity | `manifest.nodes[]` array |
| Geo metadata | `nodes[].lat/lon/region` in the manifest |
| Capability metadata | `nodes[].capabilities[]` (opaque strings) |
| Updates without on-chain tx | Manifest is off-chain HTTP — update the file, no transaction |
| Backwards compat | Plain-URL `serviceURI` is unchanged for legacy clients |
| Trust | `eth-personal-sign` over canonical bytes; resolver verifies against chain-claimed address |

## The CSV format we DO accept (read-only)

A few operators may have shipped the rejected format in the wild. Our resolver decodes it best-effort and treats the resulting nodes as `signature_status: unsigned`. The publisher in this repo will never produce CSV. See [docs/design-docs/serviceuri-modes.md](../design-docs/serviceuri-modes.md) Mode B.
