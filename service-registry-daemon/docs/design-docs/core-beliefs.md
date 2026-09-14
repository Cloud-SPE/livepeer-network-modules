---
title: Core beliefs
status: accepted
last-reviewed: 2026-09-14
---

# Core beliefs

The [monorepo core beliefs](../../../docs/design-docs/core-beliefs.md) govern
this component. These local principles describe the current architecture;
implementation gaps are identified rather than presented as enforced rules.

## Workload and trust boundaries

Capabilities and offerings are open-world strings. The registry matches them
without interpreting workload semantics. Protocol declarations are preserved
for gateways/brokers to interpret. No workload requires a registry enum entry.

The expected orchestrator address anchors trust. Discovery uses a chain pointer
or an operator-configured coordinator URL; the recovered signer must match the
expected address. Coordinator envelopes must be signed. `unsigned_allowed`
only governs unsigned static/CSV nodes, not unsigned manifests. Expiry and
publication replay enforcement remain implementation work (`lnm-dnh`).

The cold key stays on the signing host. The coordinator builds and hosts signed
publications; the cold console signs them. Registry publisher mode is
identity-only and provides no production signing route.

## Discovery and compatibility

Chain and overlay sources reuse the same verified-manifest path. Chain URL
resolution tries the exact pointer and, when applicable, standard well-known
paths. CSV and legacy endpoint synthesis remain read-only compatibility paths.
They do not guarantee that any chain URL is a usable legacy workload endpoint.

Overlay-only is a production discovery mode. Its candidate set is the enabled
configuration, not leftover chain cache entries. Static route pins remain
explicitly unsigned and separate from coordinator manifest pointers.

## Engineering constraints

- Source follows the layer stack with I/O behind providers; depguard enforces
  configured import boundaries.
- One binary mounts either resolver or identity-only publisher RPCs.
- gRPC uses a local unix socket with trusted local callers. Metrics has its own
  optional TCP listener.
- Production cache/audit storage is BoltDB. State is local to the process.
- Fresh entries avoid chain lookup and manifest fetch; live-health work may
  still run. Stale entries refresh synchronously. Round seeding is separate.
- The package coverage target remains at least 75%. The current coverage-gate
  executable is a stub and does not enforce that target (`lnm-gpd`).
- Documentation is updated alongside observable behavior. Active docs describe
  current code; historical references and completed plans remain immutable.
- Beads owns work items. Nontrivial design changes get an execution-plan design
  record; the plan is not a parallel task checklist.
