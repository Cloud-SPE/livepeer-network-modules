---
title: Legacy compatibility
stability: v1-stable
last-reviewed: 2026-04-25
---

# Legacy compatibility

Guarantees for `go-livepeer` clients that predate this daemon.

## What "legacy client" means here

A `go-livepeer`-style consumer that:
1. Reads an orchestrator's eth address from the chain.
2. Calls `ServiceRegistry.getServiceURI(address)`.
3. Treats the returned string as a URL and dials it.

These clients do not parse a manifest. They do not verify signatures. They do not understand the `/.well-known/...` path.

## What we promise legacy clients

- The on-chain `serviceURI` will continue to be a plain URL for any orchestrator that uses this daemon's publisher mode. No CSV, no base64 — just a URL.
- The URL the orchestrator publishes is the same URL the legacy client dials. Transcoding RPC behavior is unchanged on the orchestrator side.
- The publisher will never write a `serviceURI` whose first character is not a valid URL prefix (`http://` or `https://`).
- The publisher's `/.well-known/livepeer-registry.json` is at a known sub-path; legacy clients that don't request it are unaffected.

## What we do NOT promise legacy clients

- That capability advertisement via `OrchestratorInfo` gRPC continues to work. That is `go-livepeer`'s problem; this daemon does not modify it. Existing `OrchestratorInfo`-based discovery in `go-livepeer` is unchanged.
- That every orchestrator will have a manifest. Some operators may opt out and stay legacy-only. Consumers using THIS daemon's resolver get a single legacy-synthesized node for those orchestrators, with `source: "legacy"` and `capabilities: nil`.

## What we promise CSV-format orchestrators (rejected proposal compat)

A few operators may have shipped the rejected on-chain CSV format (`<url>,<version>,<base64_json>`). The resolver:
- Parses these read-only.
- Returns the structured nodes with `signature_status: "unsigned"` (CSV manifests are not signed).
- Refuses to return them unless the caller passes `allow_unsigned=true` OR the static overlay marks the eth address as `unsigned_allowed`.

This daemon's publisher will NEVER emit the CSV format.

## go-livepeer interop matrix

| go-livepeer client | This daemon's publisher | Result |
|---|---|---|
| Reads `serviceURI` for transcoding | Wrote a plain URL | Works (legacy mode) |
| Reads `serviceURI` for transcoding | Wrote a CSV (we won't) | go-livepeer probably treats first segment as URL; works incidentally. We recommend against CSV. |
| Calls `OrchestratorInfo` gRPC | Daemon doesn't run this | Untouched — go-livepeer's existing path |

## Sunset considerations

Legacy compat is committed for v1, indefinitely. Deprecation would require:
- A core-belief amendment (currently §4 protects it forever).
- A migration path with adoption metrics.
- Minimum 24-month sunset window with logged deprecation warnings on every legacy resolution.

We do not anticipate deprecating legacy compat.
