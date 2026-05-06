# DESIGN

Architectural overview at a glance. The deep version lives in
[`docs/design-docs/`](./docs/design-docs/); full provenance lives in
[`docs/references/2026-05-06-architecture-conversation.md`](./docs/references/2026-05-06-architecture-conversation.md).

## The pin

> **A workload-agnostic way of registering capabilities, payment for them, discovering
> them, and routing work to them.**

Every architectural choice in this repo flows from that requirement.

## Shape in one sentence

A single workload-agnostic process per orch host — the **capability broker** — that owns
`/registry/offerings`, dispatches paid requests over a small fixed typology of *interaction
modes* to arbitrary backends declared in YAML, with the trust spine preserved by an
operator-driven, cold-key-signed manifest publication cycle.

## Eight layers

| # | Layer | What it does |
|---|---|---|
| 1 | Capability broker | One process per host; workload-agnostic dispatcher. Replaces the three worker-node binaries. |
| 2 | Interaction-mode typology | Small fixed set of wire contracts (req/resp, stream, multipart, ws-realtime, rtmp-hls, session-control). The only place workload knowledge lives. |
| 3 | Declarative capability config | `host-config.yaml`. Define + identify + serve. Three steps, no code. |
| 4 | Discovery | Manifest is a flat list of capability tuples; coordinator UX is per-capability not per-host. |
| 5 | Trust spine | Cold key signs every manifest; secure-orch egress-only; operator drives the sign cycle. |
| 6 | Payment | `payment-daemon` decoupled from capability/work-unit enums — opaque strings, plain arithmetic. |
| 7 | Routing | Gateway has per-mode adapters, not per-capability code paths. |
| 8 | Demand visibility | Metrics at the edges; market data via independent scrapers. |

For the full sketch see
[`docs/design-docs/architecture-overview.md`](./docs/design-docs/architecture-overview.md).

## What stays sacred

- Cold orch keystore on firewalled `secure-orch`. Never moves.
- Cold-key signature on every manifest publication.
- Double-verification of signed manifest (coordinator on upload, resolver on fetch).
- On-chain orch identity (`ServiceRegistry`) is the public trust anchor.
- `payment-daemon`'s ticket validation against chain.
- Mainnet-only deployment, image-tags-not-bumped, etc. — inherited from the suite's
  core beliefs.

## What's deferred to v2

Verifiable work-unit reports, automated sign-cycle transport, warm-key opt-in, streaming
ASR, recording/DVR, multi-destination simulcast, session state across orch swap.
See [`docs/design-docs/core-beliefs.md`](./docs/design-docs/core-beliefs.md) and
[`docs/references/2026-05-06-architecture-conversation.md`](./docs/references/2026-05-06-architecture-conversation.md)
§9 for the full deferment list.
