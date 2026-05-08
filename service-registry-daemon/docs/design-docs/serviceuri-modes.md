---
title: ServiceURI resolver modes
status: accepted
last-reviewed: 2026-05-01
---

# ServiceURI resolver modes

The on-chain `ServiceRegistry.serviceURI` is a single string. Resolver deployments are configured against exactly one registry contract address, and the resolver interprets the string returned by that contract per-resolve. The primary production path is a fully qualified manifest URL; compatibility code paths remain for already-published CSV values, plus a chainless static-overlay mode when the chain has no entry.

## Mode A — well-known manifest (default for new orchestrators)

`serviceURI` is a full manifest URL such as `https://orch.example.com/.well-known/livepeer-registry.json` or `https://orch.example.com/.well-known/livepeer-ai-registry.json`.

Resolver behavior:

1. Fetch `serviceURI` verbatim (HTTP GET, size-capped, timeout-capped).
2. JSON-decode and validate per [manifest-schema.md](manifest-schema.md).
3. Verify signature per [signature-scheme.md](signature-scheme.md).
4. Cache and return the parsed `[]Node`.

If the manifest fetch fails (404, timeout, parse error, signature mismatch):
- The resolver does NOT silently fall back to legacy. It records the failure in the audit log and returns a `manifest_unavailable` error UNLESS the caller explicitly passes `allow_legacy_fallback=true` in the gRPC request.
- This is intentional: a misconfigured operator who shipped a broken manifest must see it fail, not silently degrade.

## Mode B — CSV-fallback (read-only, accommodating)

If the on-chain `serviceURI` happens to be a comma-delimited string of the form `<url>,<version>,<base64_json>` (the format from the rejected on-chain CSV proposal — see [csv-proposal-review.md](../references/csv-proposal-review.md)), the resolver decodes it best-effort and produces `[]Node`.

Resolver behavior:

1. `strings.SplitN(value, ",", 3)` — exactly 3 parts.
2. `parts[0]` is treated as the legacy URL (used as the synthesized fallback URL if the rest fails to parse).
3. `parts[1]` is parsed as a non-negative integer schema-version.
4. `parts[2]` is base64-decoded; the result is JSON-decoded into a CSV-payload struct.
5. The structured nodes are returned.

The CSV mode is **read-only**. The publisher in this repo will never *produce* a CSV `serviceURI`. The mode exists solely to interoperate with anyone who already shipped a CSV-format value.

CSV manifests are not signed. They are returned with a `signature_status: unsigned` flag on each node so consumers can apply their own trust policy (e.g., bridges may only accept unsigned data from operators whitelisted in `nodes.yaml`).

## Mode C — legacy URL synthesis (fallback)

If `serviceURI` is a URL (no CSV structure) and the manifest fetch is unavailable, AND the caller passed `allow_legacy_fallback=true`, the resolver synthesizes a single `Node`:

```go
Node{
    ID:               "legacy",
    URL:              serviceURI,
    Capabilities:     nil,         // unknown
    SignatureStatus:  "unsigned",  // by definition
    Source:           "legacy",
}
```

This is what old `go-livepeer` transcoding clients effectively get today (a URL to dial). The synthesized `[]Node` of length 1 lets a new resolver-aware consumer treat legacy and modern orchestrators uniformly.

## Mode D — static-overlay synth (chainless fallback)

If `getServiceURI(addr)` returns `not_found` AND the operator overlay carries an enabled entry for the address with at least one pin node, the resolver synthesizes a result from the overlay alone:

```go
ResolveResult{
    Mode:  "static-overlay",
    Nodes: applyOverlay(addr, nil, overlay), // pin nodes only
}
```

Use cases:

- **Bootstrap.** No orchestrator has published manifests yet, but the operator wants the resolver to serve a curated pool.
- **Static-overlay-only deployments.** A consumer running with `--discovery=overlay-only` against an empty or nonexistent chain (e.g. `--dev` mode, `examples/static-overlay-only/`).

Pin nodes default to `signature_status: unsigned`, so this mode requires `unsigned_allowed: true` on the overlay entry — otherwise the signature policy filter drops every node and the resolver returns an empty result.

This mode is reached only after a chain `not_found`. If the overlay has no entry for the address, or the entry is disabled, or the entry has no pin nodes, the resolver returns `not_found` (current behavior).

## Mode-detection algorithm

```
function detectMode(uri):
    if uri starts with "http://" or "https://":
        if uri contains "," :
            return CSV   // ambiguous: URLs may technically contain ",", but we accept the false-positive risk because a non-CSV URL with a "," is malformed-on-chain
        return WellKnown
    if uri contains exactly two "," separators:
        return CSV
    return Unknown   // logged, returned as resolver_error
```

The CSV-vs-WellKnown disambiguation is defensive: if a URL accidentally contains a comma (uncommon but RFC 3986-permitted in path segments), the CSV split will yield 1 part, and we'll re-classify as WellKnown. The actual implementation in `service/resolver/mode.go` uses `strings.Count(uri, ",")` to cheaply pre-classify.

## Static overlay precedence

After the manifest (or legacy synthesis) produces `[]Node`, the static overlay is merged. (For Mode D, the overlay *is* the source — there is no manifest to merge into.) See [static-overlay.md](static-overlay.md) for rules. Briefly:

- Static overlay wins on policy fields: `enabled`, `tier_allowed`, `weight`, `unsigned_allowed`.
- Manifest wins on advertised fields: `capabilities`, `offerings`, and
  node-level `extra`.
- Static overlay can add nodes that aren't in the manifest (operator-managed off-chain nodes).
- Static overlay cannot remove nodes from the manifest (the operator publishing is canonical for "what they advertise").
