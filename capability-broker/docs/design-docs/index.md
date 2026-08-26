# Design docs

Component-local design for the capability broker.

| Doc | Status | What it covers |
|---|---|---|
| [architecture.md](./architecture.md) | active | Package layout, HTTP surface, request lifecycle for both v1 protocols |

Cross-cutting design (the 8-layer overview, the 11 requirements, core beliefs)
lives at the repo root in [`../../../docs/design-docs/`](../../../docs/design-docs/).
Wire spec lives in [`../../../livepeer-network-protocol/`](../../../livepeer-network-protocol/).

This subfolder grows as design questions specific to the broker accumulate
(e.g., extractor sandboxing, hot-reload semantics, mock-payment behavior in
test environments).
- [`credential-store.md`](./credential-store.md) — runner attach credentials: bearer v1 in a sealed store, and the additive ed25519 keypair path (plan 0043 §3.3).
