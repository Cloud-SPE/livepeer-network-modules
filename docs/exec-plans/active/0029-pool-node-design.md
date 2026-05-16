# Plan 0029 — Pool node design

**Status:** active — design phase (paper only; no code, no `go.mod` edits)
**Opened:** 2026-05-16
**Owner:** harness
**Branch:** `feature/pool-node-design`

**Related references:**
- Kickoff conversation:
  [`../../references/2026-05-16-pool-node-conversation.md`](../../references/2026-05-16-pool-node-conversation.md)
- Architecture overview:
  [`../../design-docs/architecture-overview.md`](../../design-docs/architecture-overview.md)
- Core beliefs:
  [`../../design-docs/core-beliefs.md`](../../design-docs/core-beliefs.md)

## 1. Problem statement

Introduce a "Pool" node into the existing four-archetype paradigm
(secure-orch / orch-coordinator / worker-orch + capability-broker / gateway)
that **resells capacity from third-party orchs under its own on-chain
identity**.

The Pool is a regular orch from the outside: one `eth_address`, one signed
manifest at one well-known URL, one bond in `BondingManager`, one
secure-orch with its own cold key. The novelty is internal — its "backends"
are remote member brokers, not local containers.

## 2. Goals

- Define a Pool topology that requires **no protocol change** at the
  gateway, manifest schema, or chain-facing layers (core belief #1 —
  workload-agnostic). Pool-specific machinery stays inside the Pool's
  component set.
- Define the Pool↔member surface: registration, trust binding, health,
  payment ticket flow, settlement, and operational failure modes.
- Stay within the existing component vocabulary where possible. Introduce
  a new top-level component only if the forks below force it.

## 3. Non-goals (v1)

- The other three "pool" framings from the kickoff (multi-orch federation,
  intra-broker pool, coordinator HA cluster) — see
  [`../../references/2026-05-16-pool-node-conversation.md`](../../references/2026-05-16-pool-node-conversation.md)
  §1. Each may become its own plan later.
- Slashing of members on-chain.
- A public "pool membership registry" smart contract.
- Verifiable usage receipts from members. Core belief #7 (trust the orch's
  reported usage in v1) applies recursively to members in v1; the
  attestation slot is reserved for v2.

## 4. Initial scope — forks to resolve

| Fork | Question | Status |
|---|---|---|
| 1 | Front-proxy vs. publish-only | OPEN |
| 2 | Member-side node type | OPEN |
| 3 | Pool↔member trust binding | OPEN |
| 4 | Settlement model | OPEN |
| 5 | Manifest attribution per member | DEFERRED — revisit after forks 1+2 |

Full statement of each fork lives in the kickoff reference doc §3.

## 5. Decision log

*(Decisions land here as forks resolve. Each entry: date, fork, decision,
short rationale.)*

## 6. Open questions

- Pool economic model: fixed-rate buyer of capacity vs. revenue-share.
- Heterogeneous capability composition across members
  (per-member model/region/GPU-class differences) vs. homogeneous menu.
- Component shape: extend `orch-coordinator/` + add an edge-broker mode to
  `capability-broker/`, vs. introduce a new top-level `pool-orchestrator/`.

## 7. Out of scope (deferred to v2+)

- Cross-pool peering / pool federations.
- On-chain proof-of-membership.
- Verifiable usage proofs from members.
- Automated member onboarding / discovery (members enter via operator
  action in v1; no marketplace, no auto-enrollment).
