# Design docs

Cross-cutting design decisions for the workload-agnostic supply-side rearchitecture.

| Doc | Status | What it covers |
|---|---|---|
| [core-beliefs.md](./core-beliefs.md) | active | Invariants every change must uphold |
| [requirements.md](./requirements.md) | active | The 11 supply-side requirements with rationale |
| [architecture-overview.md](./architecture-overview.md) | active | The 8-layer proposed architecture at a glance, with mermaid diagrams per layer |
| [payment-daemon-interactions.md](./payment-daemon-interactions.md) | active | Cross-cutting guide to how the gateway, broker, and both `payment-daemon` roles interact |
| [streaming-workload-pattern.md](./streaming-workload-pattern.md) | active | Long-lived-session blueprint (broker-side meter + gateway-side ledger) for `ws-realtime`, `session-control-plus-media`, and `rtmp-…` modes |
| [payment-decoupling.md](./payment-decoupling.md) | active | What changed in `payment-daemon` for opaque capability / work-unit names |
| [backend-health.md](./backend-health.md) | active | Three-layer health model (manifest / live / failure-rate) — which layer answers which routing question |
| [trust-model.md](./trust-model.md) | active | Cold-key + sign-cycle deep dive; threat model and what each invariant defends against |
| [migration-from-suite.md](./migration-from-suite.md) | active | Suite-to-rewrite component map, phased deprecation timeline (gated by v1.0.0), and what the suite preserves long-term (cold key, on-chain identity, chain state) |
| [gpu-requirements.md](./gpu-requirements.md) | active | NVIDIA Pascal+ floor + per-vendor (Intel QSV, AMD VAAPI) matrix for the workload runners; CUDA toolkit alignment + multi-arch policy per OQ4 |
| [build-system.md](./build-system.md) | active | Canonical base images (python:3.12-slim, golang:1.22-alpine, ubuntu:24.04, nvidia/cuda:12.9.1-devel-ubuntu24.04) + image-tag pinning + buildx orchestration for the workload runners |
| [ui-design-system.md](./ui-design-system.md) | active | Shared visual system for all operator and product UIs, aligned to current Livepeer brand and explorer surfaces |
| [frontend-dom-and-css-invariants.md](./frontend-dom-and-css-invariants.md) | active | Repo-wide frontend implementation contract: light DOM only, semantic HTML only, no inline CSS, styling only from checked-in CSS files |

Stubs (to be written as we drill in):

| Doc | Status | What it will cover |
|---|---|---|
| `spec-repo-relationship.md` | stub | Boundary between this repo and `livepeer-network-protocol` |

Per-mode wire contracts live in
[`../../livepeer-network-protocol/modes/`](../../livepeer-network-protocol/modes/),
not in this directory.

Submodule-local designs live inside their respective submodules (none yet — this is a
docs-only scaffold). Promote a doc here only when it binds more than one component. If a
doc only describes one component, it belongs in that component's own `docs/`, not here.

For the full provenance of the design conversation that motivated this repo, see
[`../references/2026-05-06-architecture-conversation.md`](../references/2026-05-06-architecture-conversation.md).
