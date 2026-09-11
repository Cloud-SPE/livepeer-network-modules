---
title: Static overlay
status: verified
last-reviewed: 2026-05-19
---

# Static overlay

The resolver supports a YAML config file (`--static-overlay=/path/to/nodes.yaml`) that augments and constrains on-chain manifests. This mirrors the `nodes.yaml` posture from `openai-livepeer-bridge` — operators control their candidate pool with gitops; the registry only *adds* discovery without taking away operator authority.

## Schema

```yaml
# nodes.yaml — operator-curated overlay
overlay:
  - eth_address: "0xABCD...0123"            # required, 0x-prefixed 40-hex
    enabled: true                           # required
    tier_allowed: [free, prepaid]           # optional, list of opaque strings
    weight: 50                              # optional, integer 1-1000, default 100
    unsigned_allowed: false                 # optional, default false
    pin:                                     # optional, for nodes the operator manages off-chain
      - id: "side-channel-1"
        url: "https://internal.example.com:8935"
        capabilities:
          - name: "openai:embeddings"
            offerings:
              - id: "text-embedding-3-small"
                price_per_work_unit_wei: "100"
        tier_allowed: [prepaid]
```

## Merge precedence

Per-eth-address, after the resolver has fetched the manifest (or synthesized legacy), it merges the overlay entry:

| Field | Manifest | Overlay | Final |
|---|---|---|---|
| `eth_address` | yes | yes | manifest (overlay must match or merge skipped with audit warn) |
| `nodes[].id` | yes | yes (in `pin`) | union; ID conflict = manifest wins |
| `nodes[].url` | yes | yes (in `pin`) | manifest |
| `nodes[].capabilities` | yes | yes (in `pin`) | manifest for manifest nodes; overlay for pin nodes |
| `enabled` | n/a | yes | overlay |
| `tier_allowed` | n/a | yes | overlay (per-orchestrator-default, applied to all nodes) |
| `weight` | n/a | yes | overlay |
| `unsigned_allowed` | n/a | yes | overlay |

The principle: **the manifest is canonical for what the operator advertises; the overlay is canonical for what the consumer accepts.**

## When no manifest is present

If the resolver is in legacy or CSV mode (manifest unavailable / unsigned), the overlay is the only source of policy fields. Without an overlay entry for a given eth address:
- `enabled` defaults to `true` (resolver returns the node).
- `tier_allowed` defaults to `null` (no tier filtering).
- `weight` defaults to `100`.
- `unsigned_allowed` defaults to `false`. **In legacy/CSV mode, an absent overlay means the resolver will refuse to return the node UNLESS the caller passes `allow_unsigned=true` in the gRPC request.**

This is intentional: opt-in to trust unsigned data, never opt-out.

## Pin nodes (operator-managed off-chain)

Some operators run worker nodes that are not in any on-chain manifest. The `pin` list lets them inject those nodes into resolver results. Pin nodes always carry `source: "static-overlay"` so consumers can distinguish them.

## Chainless static-overlay mode

When the resolver is run with `--discovery=overlay-only` and the chain has no entry for an address (e.g. an unregistered orchestrator, or `--dev` mode with no chain at all), the resolver synthesizes the result purely from the overlay's pin nodes for that address. This is `ModeStaticOverlay` in [serviceuri-modes.md](serviceuri-modes.md) §"Mode D".

Two preconditions must be met or the resolver returns `not_found` instead:

- The overlay entry for the address is `enabled: true`.
- The entry has at least one entry under `pin:` — there's nothing else to serve.

Because pin nodes are unsigned by definition, `unsigned_allowed: true` on the overlay entry is also required (otherwise the signature policy filter drops every node and the resolver returns an empty `nodes` list).

In overlay-only resolver mode, the daemon walks every enabled overlay entry once at startup and pre-resolves each address. After the seed completes, `ListKnown` and `Select` see the full operator-curated pool without the consumer first calling `Refresh` or `ResolveByAddress`. Per-address seed errors are logged and swallowed — one missing manifest does not prevent the others from seeding.

## Reload

**The overlay is read exactly once, at daemon startup.** There is no file
watcher, no SIGHUP handler, and no `reload_overlay` flag on
`Resolver.Refresh` (`RefreshRequest` carries only `eth_address` and
`force`). To pick up an edited `nodes.yaml`, restart the daemon.

Do **not** send SIGHUP expecting a reload — the daemon installs handlers
only for SIGINT and SIGTERM, so SIGHUP takes the Go default disposition
and kills the process.

A parse failure at startup is fatal: the daemon refuses to boot rather
than run with a half-applied policy. The
`livepeer_registry_overlay_reloads_total` counter therefore records a
single startup load (`ok` / `io_error` / `parse_error`) rather than an
ongoing reload stream.

Hot-reload (watcher + SIGHUP + on-demand RPC flag) remains unimplemented.
(The `overlay-hot-reload-tests` entry in
`docs/exec-plans/tech-debt-tracker.md` predates this and describes it as
shipped — it is not.)

## Security

The overlay file may contain operational policy but never secrets. Lints (planned: `lint/no-secrets-in-overlay`) flag fields that look like API keys or passwords. The keystore lives elsewhere (`--keystore-path`).
