---
title: Static overlay
status: verified
last-reviewed: 2026-09-14
---

# Static overlay

The YAML file configures coordinator discovery, local policy and optional
unsigned static routes. It is loaded once at startup with strict unknown-field
rejection. A parse failure fails startup. There is no watch, SIGHUP reload or
Refresh reload flag; restart after editing the file.

## Signed coordinator discovery

```yaml
overlay:
  - eth_address: "0x0123456789abcdef0123456789abcdef01234567"
    manifest_url: "https://coordinator.example.com/.well-known/livepeer-registry.json"
    enabled: true
    weight: 100
    tier_allowed: [prepaid]
```

Only `eth_address` and `manifest_url` are needed for this flow. The address must
be a valid Ethereum address; duplicate address entries are rejected. The URL
must be absolute HTTPS without credentials or a fragment.

`manifest_url` points to the **coordinator's signed manifest endpoint**.
The manifest contains **broker URLs**, capability/offer tuples, prices and
settlement keys. It does not point to a runner, and the broker's unsigned
`/registry/offerings` response is not a signed manifest.

The URL overrides the chain pointer for this address in either discovery mode.
`--discovery=overlay-only` also uses YAML as the candidate list and constructs
no chain providers. No registry RPC or dev flag is required. Only enabled
configured addresses may resolve in that mode; old chain cache records cannot
expand the list. Chain mode may resolve other addresses independently.

The expected identity and recovered signer must match. `unsigned_allowed` does
not relax manifest verification. Signed routes use manifest source and
well-known mode, even though their location came from YAML. Work submission and
payment follow the normal broker/payment contract, with separate chain needs.

## Policy fields

| Field | Default | Effect |
|---|---|---|
| `enabled` | true | Applied to manifest nodes; selection excludes disabled nodes. Overlay-only rejects disabled addresses before resolution. |
| `tier_allowed` | unrestricted | Opaque accepted tier names; matching is case-insensitive |
| `weight` | 100 | Manifest-node ranking weight, 1–1000 |
| `unsigned_allowed` | false | Allows unsigned static/CSV nodes for this address; never an unsigned envelope |
| `pin` | empty | Append operator-asserted static route nodes |

Manifest node URLs and advertised capabilities are not overwritten by policy.
Pins are appended without ID deduplication; an identical ID is not a merge or
manifest-wins replacement. Avoid duplicate IDs in operator configuration.

## Static pins

```yaml
overlay:
  - eth_address: "0xabcdef0000000000000000000000000000000000"
    unsigned_allowed: true
    pin:
      - id: broker-static
        url: "https://broker.example.com"
        weight: 100
        capabilities:
          - name: "example:work"
            protocol: "paid-job/v1"
            work_unit: "unit"
            extra:
              job:
                transports: [unary]
            offerings:
              - id: "default"
                price_per_work_unit_wei: "100"
                per_units: 10
```

`pin.url` is a direct route endpoint, **never fetched as a manifest pointer**.
ID and URL are required. Capabilities/offerings are optional to parse, but must
be supplied for a matching Select call. The parser requires capability names
and offering IDs, but does not fully validate pin protocol, URL scheme, price
or billing-unit completeness. Do not mistake accepted YAML for a payable route.
The old `interaction_mode` and `warm` fields are rejected.

Pin-specific tier policy overrides the parent when supplied. Pin weight defaults
to 100 independently of parent weight, so set it explicitly when a different
pin rank is intended. Pin capability extra is preserved; pins have no signed
settlement delegation. Each pin is marked unsigned and static-overlay source.

Unsigned static/CSV nodes require `unsigned_allowed`, daemon
`--reject-unsigned=false`, or an explicit Resolve request allowance. Select and
SelectMany obey daemon/overlay policy. Legacy synthesized nodes have a distinct
legacy status rather than unsigned status.

With no manifest pointer and no chain entry, an enabled entry containing pins
can produce a static-only result. Empty/missing/disabled entries yield
not_found; unsigned filtering can yield a successful result with zero nodes.
In overlay-only mode the chain is bypassed entirely. Existing deployments that
used overlay-only to look up serviceURI must add `manifest_url` or use chain
mode.

## Refresh and failure behavior

Startup warms enabled entries best-effort. Failed coordinator fetches are
retried by subsequent Select/SelectMany and Refresh calls. ListKnown lists
cached candidates only. Manifest TTL refresh is synchronous on demand, not a
background polling loop. Forced Refresh fetches immediately.

Source-aware cache entries prevent reuse when a configured pointer changes or
is removed. Transport failure may use the same source's verified last-good
publication within max-stale. Invalid publications do not use that fallback;
explicit manifest pointers never downgrade to legacy nodes. See
[cache behavior](resolver-cache.md) and
[manifest enforcement limits](../product-specs/manifest-contract.md).

Keep secrets out of overlay files. Manifest URLs cannot embed credentials;
keystores and authentication credentials belong to their separate components.
