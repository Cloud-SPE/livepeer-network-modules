# PRODUCT_SENSE

What this is, who it's for, what problems it solves, and what it explicitly is not.

## Who this is for

The **Livepeer orchestrator-operator** — the human (and infra) running supply-side
capacity on the Livepeer network. They have:

- A heterogeneous mix of GPU servers (single-box, LAN'd, geo-scattered).
- Sometimes third-party API credentials they want to resell (OpenRouter / OpenAI /
  Anthropic).
- A range of network postures (publicly addressable, NAT'd home LAN, behind a CDN/LB).
- Industry-standard ingress tools (Cloudflare Tunnel, Traefik, Cloudflare LB) — they do
  not need the Livepeer stack to solve NAT traversal.

Secondary audience: **the gateway operator** and **third-party developers** who want to
introduce a new capability type without coordinating a release of the canonical
livepeer-modules trunk.

## What it is

A **workload-agnostic supply-side rearchitecture**:

- One host process (the *capability broker*) that owns the host's
  `/registry/offerings` regardless of what the underlying compute looks like.
- A small fixed typology of *interaction modes* that captures every wire shape we care
  about (req/resp, streaming, realtime, RTMP, sessions). New capabilities pick a mode;
  no code changes elsewhere.
- A single declarative `host-config.yaml` as the operator's entire day-to-day surface.
- A trust spine that's identical to today's (cold-key-signed manifests, on-chain
  identity, double verification) — the rearchitecture *preserves* it, doesn't change it.

## What it is not

- **Not a chain change.** No new on-chain contracts; we keep `ServiceRegistry`,
  `TicketBroker`, `BondingManager`, `RoundsManager` on Arbitrum One. Mainnet-only.
- **Not a payment-protocol change.** `payment-daemon` keeps its sender/receiver shape;
  the only delta is the catalog opens up to opaque strings instead of a closed enum.
- **Not a NAT-traversal solution.** Operators front their backends with Cloudflared,
  Traefik, or a public LB; the broker just speaks HTTP/RTMP at whatever URL ends up
  publicly reachable.
- **Not a verifiable-compute system.** v1 trusts the orch's reported usage. Verifiability
  hooks are reserved in the schema; the implementation is v2 work.
- **Not a Pipeline-side rebuild.** The customer-facing apps (vtuber-project, openai
  shell, video gateway) are out of scope — they consume gateways the same way external
  customers do.
- **Not a replacement for orch-coordinator's signing flow.** `secure-orch` remains
  egress-only; operator hand-carries signed manifests. (May change in v2; not now.)
- **Not a fork or successor of `livepeer-network-suite`.** That repo continues to ship
  and is not modified by anything done here. The two share no submodules, no pinned
  SHAs, and no release schedule. This repo's first release is v1.0.0; the suite's
  release line is independent.

## Anti-goals

- **No tight coupling between worker code and a livepeer-modules library.** The contract
  is the wire spec, not a Go library. Implementers choose any language; reference impls
  are opt-in.
- **No closed enum of capability identifiers or work-unit names.** Anyone (orch, gateway,
  external developer) can invent a capability string.
- **No declared capacity numbers in the manifest.** Capacity is gameable and meaningless
  cross-workload. Workers fail loudly with 503 when full; gateway routes around.
- **No automated push to secure-orch.** Hard rule for v1. Operator drives the sign cycle.
- **No backwards-compatibility shims for the existing three-worker-binary shape.** The
  old shape is the problem we're solving; we don't preserve it as a "legacy mode."
- **No new key material for routine operations.** Cold key signs every change, full stop.
  Warm-key is rejected for v1.
- **No automatic code carryover from the existing suite.** Material from
  `livepeer-network-suite` or its submodules is copied in only on explicit user
  instruction. Each copy is a deliberate, commit-recorded decision.

## Why this matters

The current suite's worker-binaries-by-workload-family shape forces operators to run
multiple binaries on a single host, multiplies coordinator roster slots, and tightly
couples every new capability to a `livepeer-modules-project` release. The "server-2
incident" — needing audio + vtuber on one host and finding the coordinator could not
register both — is the canonical example. This repo exists to make that incident
impossible by construction.
