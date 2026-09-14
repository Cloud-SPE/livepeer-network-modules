---
title: Discovery sources and resolver modes
status: verified
last-reviewed: 2026-09-14
---

# Discovery sources and resolver modes

Source selection happens before wire-format detection. An overlay
`manifest_url` overrides the chain pointer for that address. In overlay-only
mode, YAML supplies the enabled candidate list and no chain provider is built.
In chain mode, addresses are seeded from the active pool on round events and
may also be resolved explicitly by callers.

## Signed manifest URLs

The resolver fetches the configured URL first. If it does not contain
`/.well-known/`, it also tries the same origin's
`/.well-known/livepeer-registry.json` and
`/.well-known/livepeer-ai-registry.json`, in that order, until a valid signed
manifest is found. This supports both full document pointers and older base
URLs. A malformed candidate does not become a trusted manifest; validation
errors are preserved if later candidates are merely unreachable.

Envelope decoding and signature recovery are identical for overlay and chain
sources. Successful results use domain mode `well-known`, and manifest nodes
use source `manifest`, even when the URL came from YAML. No unsigned option
allows an unsigned coordinator envelope. See [manifest contract](../product-specs/manifest-contract.md).

## Chain lookup and detection

When `--ai-service-registry-address` is nonempty, that contract is the sole
pointer source. Its default is the Arbitrum AI registry. Set the flag to an
empty string to use primary `ServiceRegistry`, whose address comes from
Controller unless overridden. There is no fallback between the two contracts.
All configured production discovery RPC endpoints must match `--chain-id`
at startup. A provider error is not the same as not-found.

`internal/service/resolver/mode.go` trims the pointer and counts commas:

- Zero commas: accepted URL → signed-manifest mode.
- Exactly two commas: accepted first URL, numeric nonnegative middle segment,
  and nonempty final segment → CSV mode.
- Anything else: `unknown_mode`.

The URL helper accepts HTTPS and a localhost/127.0.0.1-prefixed HTTP host.
Overlay `manifest_url` is stricter: absolute HTTPS with no userinfo or fragment.
Use HTTPS outside test fixtures. The shared fetcher's insecure-redirect switch
is enabled only in dev; it is not a complete source URL validation boundary.

## CSV compatibility

A CSV pointer has `<url>,<version>,<base64-json>` shape. The resolver accepts
standard base64 and raw URL-safe base64. The JSON payload has `nodes[]` with
`url`, or `ip` plus positive `port`; nodes with no usable URL are skipped.
`capabilitiesUrl` is parsed but never fetched. Capabilities/prices are not
synthesized from CSV. Bad base64/JSON returns a parse error; it does not silently
fall back to the first URL. Returned nodes are unsigned with `csv-fallback`
source. This daemon never writes CSV or any other chain pointer.

## Legacy synthesis

For a chain URL, `allow_legacy_fallback=true` permits synthesis when manifest
fetching is unavailable or too large and no usable signed last-good result
was returned. The node has ID `legacy`, URL equal to the original chain
pointer, signature status `legacy`, and no capabilities or settlement keys.
It cannot satisfy capability/offering selection on its own. Invalid
publications do not authorize this fallback.

## Static pins

When no explicit manifest URL exists and the chain has no entry (or is bypassed
in overlay-only mode), an enabled overlay with pins can synthesize static
nodes. Pins carry unsigned status and static-overlay source; policy decides
whether they may be returned. Without usable configuration the result is
`not_found`.

Domain mode `static-overlay` currently maps to wire enum
`RESOLVE_MODE_UNSPECIFIED`, because the proto has no static-overlay mode enum.
Consumers can distinguish pins using node source `SOURCE_STATIC_OVERLAY`.
This does not affect overlay-fetched signed manifests, which use WELL_KNOWN.

## Policy and failure handling

[Static overlay](static-overlay.md) defines policy and pin behavior.
[Resolver cache](resolver-cache.md) defines TTL, refresh and bounded last-good
reuse. A loaded overlay does not by itself guarantee selectable routes: signed
publication, matching capability/offering, local policy, usable prices and
broker health all matter.
