---
title: Interaction protocols
status: active
last-reviewed: 2026-08-19
---

# Interaction protocols

Cross-cutting contract for the gateway↔broker wire shapes. Rebuilt 2026-08:
the seven enumerated "interaction modes" are replaced by **two protocols plus
declared axes**. The architectural rule survives unchanged:

- capabilities are workload-specific
- protocols are not
- gateways and the broker implement each protocol once and reuse it across
  every capability

## Why the mode taxonomy died

A mode name bundled five independent decisions — transport shape, session
lifetime, media-plane location, metering source, payment cadence — into one
enumerated string. The mode-space is combinatorial but the naming was
enumerative: every new combination either spawned a new mode or was shoehorned
into an old name (`live-session-remote-runner@v0` famously carried two
contradictory implementations). Consumers hard-coded mode lists, gateways
smuggled mode selection through offering names, and workload identity leaked
into protocol names.

The factoring that replaced it: the only distinction every counterparty
actually gates on is **job vs session**. Everything else is a declared,
per-offering axis.

## The two protocols

| Protocol | Contract | Spec |
|---|---|---|
| `paid-job/v1` | One paid exchange, settled once. Transport (`unary` \| `stream` \| `multipart`) negotiated per-request. Idempotent open on `Livepeer-Request-Id`. | [`protocols/paid-job.md`](../../livepeer-network-protocol/protocols/paid-job.md) |
| `paid-session/v1` | Durable paid session: runtime described by a capability-owned descriptor schema, standard control plane (top-up / status / end, optional control-WS), cumulative usage claims, funding-linked lease, fail-closed recovery. | [`protocols/paid-session.md`](../../livepeer-network-protocol/protocols/paid-session.md) |

Supporting specs:

- [`protocols/runtime-descriptor.md`](../../livepeer-network-protocol/protocols/runtime-descriptor.md)
  — the descriptor framework: schema tags, public/private partition, one-time
  admission grants. **Workload identity lives here, not in protocol names.**
- [`protocols/offering-axes.md`](../../livepeer-network-protocol/protocols/offering-axes.md)
  — the declared axes (`transports`, `descriptor_schema`, `attachment`,
  `metering`, `refill`, `heartbeat`, `lease`, economics hints) and the table
  of who consumes which.
- [`descriptors/`](../../livepeer-network-protocol/descriptors/) — one spec
  per descriptor schema (`sfu-room/v1`, `rtmp-hls/v1`, `scope-passthrough/v1`,
  `trickle-egress/v1`, …).
- [`dual-meter-trust.md`](./dual-meter-trust.md) — the trust model the claim,
  balance, and divergence semantics rest on.

## How a new capability picks its shape

1. One exchange or a long-lived engagement? That is `paid-job` vs
   `paid-session` — the whole decision.
2. Session work: is there an existing descriptor schema for the runtime's
   shape? If yes, config only. If no, author a schema under `descriptors/` —
   implemented by the runner that emits it and the gateway that consumes it,
   with no broker, clearinghouse, or registry change.
3. A new *protocol* is the rare, expensive case and needs a plan; the default
   answer to "this doesn't fit" is a new descriptor schema, not a new
   protocol.

## Cross-stack implications

- `capability-broker` owns two engines (job, session) — not one driver per
  workload.
- Gateways implement two protocol clients plus the descriptor schemas they
  serve.
- `service-registry-daemon` / `orch-coordinator` pass `protocol` and the axes
  through opaquely.
- The clearinghouse gates on `protocol` and `session.refill` only.
- `payment-daemon` remains protocol-agnostic; only the caller's lifecycle
  differs.
