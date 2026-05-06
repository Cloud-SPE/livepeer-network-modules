# Design docs

Cross-cutting design decisions for the workload-agnostic supply-side rearchitecture.

| Doc | Status | What it covers |
|---|---|---|
| [core-beliefs.md](./core-beliefs.md) | active | Invariants every change must uphold |
| [requirements.md](./requirements.md) | active | The 11 supply-side requirements with rationale |
| [architecture-overview.md](./architecture-overview.md) | active | The 8-layer proposed architecture at a glance |

Stubs (to be written as we drill in):

| Doc | Status | What it will cover |
|---|---|---|
| `interaction-modes.md` | stub | Detailed wire-contract specs for the initial 6 modes |
| `backend-health.md` | stub | Three-layer health model (manifest / live / failure-rate) |
| `spec-repo-relationship.md` | stub | Boundary between this repo and `livepeer-network-protocol` |
| `payment-decoupling.md` | stub | What changes in `payment-daemon` to support opaque names |
| `migration-from-suite.md` | stub | Deprecation timeline for existing `*-worker-node` repos |

Submodule-local designs live inside their respective submodules (none yet — this is a
docs-only scaffold). Promote a doc here only when it binds more than one component. If a
doc only describes one component, it belongs in that component's own `docs/`, not here.

For the full provenance of the design conversation that motivated this repo, see
[`../references/2026-05-06-architecture-conversation.md`](../references/2026-05-06-architecture-conversation.md).
