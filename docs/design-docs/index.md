# Design docs

Cross-cutting design decisions for the workload-agnostic supply-side rearchitecture.

| Doc | Status | What it covers |
|---|---|---|
| [core-beliefs.md](./core-beliefs.md) | active | Invariants every change must uphold |
| [requirements.md](./requirements.md) | active | The 11 supply-side requirements with rationale |
| [architecture-overview.md](./architecture-overview.md) | active | The 8-layer proposed architecture at a glance, with mermaid diagrams per layer |
| [container-platform-audit.md](./container-platform-audit.md) | active | Base-image/version matrix, duplication audit, canonical version manifest, and the target normalization path for Docker builds |
| [interaction-modes.md](./interaction-modes.md) | active | The finite set of gateway↔broker wire shapes and when each mode is the right fit |
| [payment-daemon-interactions.md](./payment-daemon-interactions.md) | active | Cross-cutting guide to how the gateway, broker, and both `payment-daemon` roles interact |
| [streaming-workload-pattern.md](./streaming-workload-pattern.md) | active | Long-lived-session blueprint (broker-side meter + gateway-side ledger) for `ws-realtime`, `session-control-plus-media`, and `rtmp-…` modes |
| [payment-decoupling.md](./payment-decoupling.md) | active | What changed in `payment-daemon` for opaque capability / work-unit names |
| [pricing-overview.md](./pricing-overview.md) | active | End-to-end synthesis: how price flows from `host-config.yaml` through manifest, discovery, headers, extractors, modes, session, debit, settlement, and pool receipts |
| [backend-health.md](./backend-health.md) | active | Three-layer health model (manifest / live / failure-rate) — which layer answers which routing question |
| [trust-model.md](./trust-model.md) | active | Cold-key + sign-cycle deep dive; threat model and what each invariant defends against |
| [migration-from-suite.md](./migration-from-suite.md) | active | Suite-to-rewrite component map, phased deprecation timeline (gated by the first rewrite release tag), and what the suite preserves long-term (cold key, on-chain identity, chain state) |
| [gpu-requirements.md](./gpu-requirements.md) | active | NVIDIA Pascal+ floor + per-vendor (Intel QSV, AMD VAAPI) matrix for the workload runners; CUDA toolkit alignment + multi-arch policy per OQ4 |
| [build-system.md](./build-system.md) | active | Canonical base images (python:3.12-slim, nvidia/cuda:12.9.1-runtime-ubuntu24.04, golang:1.25.7-alpine, ubuntu:24.04, plus CUDA devel where build stages require it) + image-tag pinning + buildx orchestration for the workload runners |
| [ui-design-system.md](./ui-design-system.md) | active | Shared visual system for all operator and product UIs, aligned to current Livepeer brand and explorer surfaces |
| [frontend-dom-and-css-invariants.md](./frontend-dom-and-css-invariants.md) | active | Repo-wide frontend implementation contract: light DOM only, semantic HTML only, no inline CSS, styling only from checked-in CSS files |
| [pool-node-production-readiness.md](./pool-node-production-readiness.md) | active | Cross-cutting production gate for the Pool stack: persistence, secrets, retry policy, alerting, privacy, and live runtime validation |
| [pool-orchestrator-production-rollout.md](./pool-orchestrator-production-rollout.md) | active | Cross-cutting operator rollout path for the Pool-based orch: controller, broker apply, coordinator publish, and secure-orch sign cycle |

Stubs (to be written as we drill in):

| Doc | Status | What it will cover |
|---|---|---|
| `spec-repo-relationship.md` | stub | Boundary between this repo and `livepeer-network-protocol` |

Per-mode wire contracts live in
[`../../livepeer-network-protocol/modes/`](../../livepeer-network-protocol/modes/),
not in this directory.

Component-local designs live inside their respective submodules. Promote a doc
here only when it binds more than one component. If a doc only describes one
component, it belongs in that component's own `docs/`, not here.

For the full provenance of the design conversation that motivated this repo, see
[`../references/2026-05-06-architecture-conversation.md`](../references/2026-05-06-architecture-conversation.md).
