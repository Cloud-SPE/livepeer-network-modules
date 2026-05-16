# Pool node conversation — 2026-05-16

Source-of-truth capture of the kickoff design conversation for a "Pool node"
concept in the Livepeer Network rewrite paradigm. This is a **reference**
document — point-in-time, not edited after the fact. Live design decisions
land in [`../exec-plans/active/0029-pool-node-design.md`](../exec-plans/active/0029-pool-node-design.md).

> Participant framing: the user opened an open-ended planning discussion to
> explore introducing a new "Pool" node type into the existing four-archetype
> paradigm. No code, no schema changes, no component scaffolding yet — pure
> design conversation, captured on branch `feature/pool-node-design`.

---

## 1. Framing chosen

The harness presented four candidate framings for what "Pool node" could mean
inside the existing paradigm:

- **Multi-orch federation** — N independent orchs (each with own
  keystore/coordinator) aggregate offerings under one combined manifest;
  Pool fronts the gateway-facing surface; revenue splits among members.
- **Intra-orch broker pool** — one operator runs many capability-broker
  hosts; a Pool node sits between the coordinator's manifest tuples and the
  brokers, presenting one logical `worker_url` and load-balancing across
  the broker fleet.
- **Coordinator-cluster pool** — multiple coordinator instances pooled for
  HA/failover behind one published manifest URL.
- **Pool-as-product (resold capacity)** — the Pool is itself an orch on-chain
  with its own `eth_address`; its "brokers" are remote third-party orchs'
  brokers under contract; Pool resells aggregated capacity under its own
  on-chain identity.

**User selection:** **Pool-as-product (resold capacity)**.

The other three framings are explicitly *not* the subject of this workstream.
They may be revisited in their own future plans if useful.

---

## 2. Architectural framing carried forward

Under Pool-as-product the Pool is **just another orch from the outside**:

- One `eth_address` registered on `ServiceRegistry` / `AIServiceRegistry`.
- One signed manifest at one well-known URL.
- Own cold key, secure-orch, coordinator, manifest sign cycle.
- Posts its own bond to `BondingManager`; walked by gateways via
  `GetFirstTranscoderInPool` like any other orch (see
  [`../design-docs/architecture-overview.md`](../design-docs/architecture-overview.md)
  line 409).

The novelty is **inside** the Pool: a Pool broker's "backends" are no longer
local containers or LAN runners — they are remote third-party orchs' brokers
under some form of membership contract. The other archetypes (secure-orch,
coordinator) carry over unchanged in their public-facing semantics.

This framing is deliberately the *only* one that does not — at first cut —
require a protocol change. The Pool is invisible to existing gateways, the
manifest schema, and the on-chain registries. All Pool-specific machinery is
internal to the Pool's own component set.

---

## 3. The five forks identified at kickoff

Five design forks were surfaced as load-bearing on scope, trust, and economics:

1. **Front-proxy vs. publish-only.**
   - *(a) Front-proxy:* gateway → Pool's edge-broker → forward to member's
     broker → backend. Payment terminates at the Pool's receiver daemon.
     Tuples stay 1:1 with `eth_address`. No protocol change.
   - *(b) Publish-only:* Pool publishes a manifest whose `worker_url`s point
     directly at member brokers' WAN endpoints, but tickets remain payable
     to the Pool's `eth_address`. Breaks the `worker_url ↔ eth_address`
     correspondence at Layer 1 and would require a new
     "ticket-receiver-is-not-worker-host" mode in `payment-daemon`.

2. **Member-side node type.** What a member operator actually runs:
   - Full capability-broker + a new `pool-member-agent` for
     registration/auth/heartbeat with the Pool's coordinator.
   - A stripped "thin broker" that only speaks the Pool flavor of the
     protocol (no secure-orch, no on-chain identity, no own manifest).
   - Nothing new — a member is a regular orch the Pool whitelists as a
     backend, with pricing/settlement entirely out-of-band.

3. **Pool↔member trust binding.**
   - mTLS + out-of-band contract (simplest, no protocol change).
   - Member signs a `pool-membership.json` with their orch key; Pool's
     coordinator verifies (reuses existing keystore primitives).
   - A new on-chain "Pool membership" registry (largest blast radius;
     likely v3, not v1).

4. **Settlement with members.** Pool's receiver daemon credits a single
   `eth_address` (the Pool's). Member payouts are either:
   - Internal ledger keyed by member; periodic off-chain payouts per a
     written agreement (USDC, ETH, fiat).
   - Pool's payment-daemon grows a "split" feature that, on ticket
     redemption, forwards a fraction on-chain to the member's address.

5. **Attribution in the published manifest.** Whether a tuple carries
   `originated_by_member=<opaque-id>` in `extra`:
   - *Yes:* third-party Layer 8 scrapers can see member granularity;
     advertises Pool composition.
   - *No:* members are invisible to gateways; Pool looks monolithic.

**User selection:** tackle forks **1, 2, 3, and 4** as the initial scope of
plan 0029. Fork 5 is deferred until forks 1 and 2 are settled (manifest
attribution only makes sense once we know whether the Pool is front-proxy
or publish-only, and what shape the member presents).

---

## 4. Open questions raised at kickoff

These are not forks (they don't gate scope) but they shape the design and
need answers before plan 0029 leaves the design phase:

- **Economic model.** Is the Pool a fixed-rate buyer of capacity (Pool pays
  members $X per work-unit hour and pockets the spread against market
  price), or a revenue-share (Pool takes Y% of each redeemed ticket and
  forwards the remainder to members)? The plumbing differs.
- **Heterogeneous capability composition.** Do different members serve
  different models / regions / GPU classes for the same `capability_id`
  (so the Pool's published manifest has multiple tuples per capability,
  one per member-shape), or do all members serve a homogeneous menu and
  the Pool load-balances across them?
- **Component shape for v1.** Is this a feature inside `orch-coordinator/`
  plus a new "edge-broker" mode of `capability-broker/`, or a new
  top-level component (`pool-orchestrator/`) sitting alongside them?

---

## 5. Workstream decisions at kickoff

- **Branch:** `feature/pool-node-design`, opened off `main` at
  commit `fe8188ea`.
- **Reference doc:** this file (`2026-05-16-pool-node-conversation.md`).
- **Active plan:** `docs/exec-plans/active/0029-pool-node-design.md`.

Subsequent design decisions land in plan 0029 (which is editable and tracks
the live decision log). Shipped material — once the component takes shape —
is promoted to `docs/design-docs/` per the repo's doc-gardening expectations.
