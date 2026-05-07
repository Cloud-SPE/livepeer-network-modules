# Design docs

Cross-cutting design decisions for the workload-agnostic supply-side rearchitecture.

| Doc | Status | What it covers |
|---|---|---|
| [core-beliefs.md](./core-beliefs.md) | active | Invariants every change must uphold |
| [requirements.md](./requirements.md) | active | The 11 supply-side requirements with rationale |
| [architecture-overview.md](./architecture-overview.md) | active | The 8-layer proposed architecture at a glance |
| [migration-from-suite.md](./migration-from-suite.md) | active | Suite-to-rewrite component map, phased deprecation timeline (gated by v1.0.0), and what the suite preserves long-term (cold key, on-chain identity, chain state) |
| [gpu-requirements.md](./gpu-requirements.md) | active | NVIDIA Pascal+ floor + per-vendor (Intel QSV, AMD VAAPI) matrix for the workload runners; CUDA toolkit alignment + multi-arch policy per OQ4 |
| [build-system.md](./build-system.md) | active | Canonical base images (python:3.12-slim, golang:1.22-alpine, ubuntu:24.04, nvidia/cuda:12.9.1-devel-ubuntu24.04) + image-tag pinning + buildx orchestration for the workload runners |

Stubs (to be written as we drill in):

| Doc | Status | What it will cover |
|---|---|---|
| `interaction-modes.md` | stub | Detailed wire-contract specs for the initial 6 modes |
| `backend-health.md` | stub | Three-layer health model (manifest / live / failure-rate) |
| `spec-repo-relationship.md` | stub | Boundary between this repo and `livepeer-network-protocol` |
| `payment-decoupling.md` | stub | What changes in `payment-daemon` to support opaque names |

Submodule-local designs live inside their respective submodules (none yet — this is a
docs-only scaffold). Promote a doc here only when it binds more than one component. If a
doc only describes one component, it belongs in that component's own `docs/`, not here.

For the full provenance of the design conversation that motivated this repo, see
[`../references/2026-05-06-architecture-conversation.md`](../references/2026-05-06-architecture-conversation.md).
