# Requirements

The eleven supply-side requirements derived from the architectural conversation. Every
design choice in this repo must be evaluable against this list. Adding to or removing
from this list is a load-bearing change — open a plan first.

For the full conversation provenance, see
[`../references/2026-05-06-architecture-conversation.md`](../references/2026-05-06-architecture-conversation.md).

## R1 — Workload-agnostic register / pay / discover / route

The pin. The architecture must support registering capabilities, paying for them,
discovering them, and routing work to them **without the architecture itself knowing
what the workload is**. Workload-specific knowledge lives in the orch's config and the
gateway's per-mode adapters; nowhere else.

**Why:** the existing suite couples capabilities to worker binaries to coordinator
roster slots to payment-daemon catalogs. Every new capability is a multi-repo trunk
PR. This requirement is the inverse.

## R2 — Heterogeneous backends across heterogeneous topologies

Backends may be: self-hosted GPU containers (single-box / LAN'd / scattered) or
third-party API resale (OpenRouter, OpenAI, Anthropic). Reachability may be public,
NAT'd, or only outbound.

**Why:** orchs already solve ingress with off-the-shelf tools (Cloudflare Tunnel,
Traefik, Cloudflare LB). The architecture should not duplicate that work.

**Implication:** advertised `worker_url` is any public TLS endpoint that speaks the
worker protocol. No NAT traversal, no relay, no hole-punching in the architecture.

## R3 — Capability swappable at runtime

The orch must be able to reprice, repurpose, or replace a capability on existing
hardware **without rebuilding a binary**. Workload is data, not code.

**Why:** demand on the network shifts; orchs need to follow it. Static-on-startup
worker binaries kneecap demand-following.

## R4 — Demand visibility

The orch must be able to see *what to switch toward*. The architecture provides exposed
metrics surfaces; aggregation is third-party.

**Why:** "switch from a lesser-used to a higher-demand workload" presupposes visible
demand. The current architecture has no surface for it.

## R5 — Three-step, code-free, declarative operator workflow

Day-to-day operator gesture: **define** capabilities + price → **identify** servers →
**serve**. No Go module written. No binary rebuilt. No trunk PR. No hand-carry per
reprice. Restarts at most.

**Why:** friction is the silent killer of orch operator adoption. If onboarding a
capability takes a week, orchs only run the capabilities the suite ships with.

## R6 — Trust anchor preserved

Cold-key-signed advertisements; gateway verifies before trusting capacity / capability
/ price; work arrival authenticated via payment ticket. New flexibility cannot create
a spoofing surface.

**Why:** orchs are deliberate about offerings and pricing. Spoofing a price = stealing
revenue or sabotaging reputation. The current trust spine works; preserve it.

## R7 — Open-world capability identifiers

Capability IDs are opaque strings. Anyone can invent one. No canonical schema registry.
Convergence is by convention or market pressure.

**Why:** capability semantics evolve faster than canonical registries. A registered-only
model becomes a chokepoint.

## R8 — Typology of interaction modes

A small fixed set of wire-contract templates (req/resp, stream, multipart, ws-realtime,
rtmp-hls, session-control). Capabilities self-declare which mode they use. Gateways
implement per-mode adapters; capabilities are opaque to gateway code.

**Why:** R8 is what makes R1 possible. Without a typology, gateway code has to
understand each capability — workload-agnosticism collapses. With a typology, workload
knowledge is contained.

## R9 — v1 trusts orch-reported usage

Worker reports `actualUnits` after `Serve`; gateway debits. No cap-and-bound estimator.
No signed-receipt machinery. Schema reserves hooks for v2 verifiability.

**Why:** verifiability is hard. Trust + market dynamics get us to a working v1 fast.
Verifiability is the v2 question.

## R10 — Metrics exposed at edges; market data via independent scrapers

Components expose Prometheus on a documented schema. Independent third parties build
public market dashboards. Architecture publishes surfaces; doesn't aggregate.

**Why:** centralized aggregation is a chokepoint and a privacy concern. The
permissionless ethos applies.

## R11 — Cold key + operator-driven sign cycle

`secure-orch` is egress-only. Operator drives the cycle (download candidate → sign →
upload signed). Cold key signs every change. Friction reduction is in console UX
(diff, one-click sign), not in the transport. No warm key for v1.

**Why:** cold-key compromise is catastrophic. Operators are protective for good reason.
Automation must not change blast radius.

---

## Crosswalk to architecture layers

| Requirement | Primary layer(s) addressing it |
|---|---|
| R1 | Whole architecture; especially L1 broker, L4 discovery, L6 payment |
| R2 | L1 broker (backend descriptors); L3 config |
| R3 | L3 config + reload semantics |
| R4 | L8 metrics |
| R5 | L3 config (the entire surface) |
| R6 | L5 trust spine |
| R7 | L4 discovery (manifest schema); L6 payment (opaque names) |
| R8 | L2 mode typology |
| R9 | L6 payment (no estimator); L2 mode typology (extractor library) |
| R10 | L8 metrics |
| R11 | L5 trust spine |

(Layers refer to the 8-layer overview in
[`architecture-overview.md`](./architecture-overview.md).)
