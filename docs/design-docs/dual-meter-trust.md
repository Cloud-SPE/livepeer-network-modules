---
title: Dual-meter trust — metering, billing, and bounded exposure
status: active
last-reviewed: 2026-08-18
---

# Dual-meter trust

How usage gets metered, who believes which number, and why nobody has to
trust anybody very much. This is the economic-trust companion to
[trust-model.md](./trust-model.md) (which covers key custody and the sign
cycle) and the authority the v1 protocol specs
([`paid-job/v1`](../../livepeer-network-protocol/protocols/paid-job.md),
[`paid-session/v1`](../../livepeer-network-protocol/protocols/paid-session.md))
reference for their claim, balance, and divergence semantics.

Decisions recorded here were locked 2026-08-18.

## 1. The principle

There is no neutral meter in this system, and the design stops pretending
there could be one. The broker is orchestrator-owned: when it meters a
session, **the seller is metering the sale**. The gateway fronts the
customer: when it measures anything, the buyer is measuring the purchase.
Every architecture that routed one party's meter to the other party's ledger
produced the same result in practice — the receiving side quietly ignored the
number and billed on estimates.

So the rule is:

> **Each side holds its own meter to protect itself. Neither trusts the
> other's. Money moves in increments small enough that lying is only ever
> worth one increment.**

Three legs:

1. **The seller's meter** (broker debit loop) exists to *stop unfunded work*.
   Fail-closed: no funded runway, no running runtime. It is not a billing
   source for anyone else.
2. **The buyer's funding cadence** bounds exposure. Runway is released in
   increments; a dishonest counterparty is worth at most the outstanding
   increment before funding stops. Enforcement is economic — defund,
   deselect, reroute — never adjudication.
3. **The buyer's own meter** bills the customer. It runs at the gateway's
   admission edge and touches no media.

## 2. Meter at the admission edge, never at the media plane

The gateway keeps the **authorization edge**: it holds the credentials by
which customers attach to a runtime — issued to it via the runtime
descriptor's one-time admission grants — and so has first-party knowledge of
who attached, when, and for how long. That signal is the buyer's meter of
record, and it exists for every use case without the gateway ever handling
media:

| Use case | Buyer's meter of record (admission edge) |
|---|---|
| Meetings / SFU rooms | Participant-token mints + token TTL refreshes → participant-minutes |
| Live transcode | Stream-key issuance + playback probe → wall-clock stream time |
| LLM / job work | The response transits the gateway → usage object, byte counts, own clock |
| Generative interactive (scope-like) | Signaling/control proxied at the gateway → session-attached time |
| Avatar / egress pipelines | Session bearers minted by the gateway → session duration |

The precision limit is acknowledged: the admission edge sees *duration and
attachment*, not compute intensity. For units finer than the edge can see
(tokens, GPU-seconds-at-resolution), the gap is carried by the tolerance
band (§4) and, for high-value offerings, by sampling — spot-verifying a
deliverable — rather than by continuous metering.

## 3. Claims are not bills

Everything the seller's side reports — `Livepeer-Work-Units` on a job,
cumulative `usage.total` on session events, the `balance` object — is a
**claim**. Claims are consumed for exactly two purposes:

- **runway accounting**: deciding when to top up and how much headroom
  remains, and
- **divergence detection**: comparing against the buyer's edge signal.

Claims are never the customer's bill. The protocol specs label this
explicitly so no future gateway "simplifies" itself back into billing on the
counterparty's meter.

## 4. The divergence policy

Per offering, the seller declares a **tolerance band**
(`session.tolerance_band_pct` in the offering axes). The locked policy:

> **Bill customers from the edge signal; pay orchestrators per claims while
> claims stay inside the band; the gateway absorbs the spread as cost of
> business, priced into margin.**

Consequences, all deliberate:

- The band is a **pricing parameter, not a security parameter**. Honest
  disagreement (clock skew, rounding, a dropped tick) is margin math — there
  is no reconciliation handshake, no clawback path, no arbitration state
  machine anywhere in the wire contract.
- The band **edge** is the only protocol-visible event: claims persistently
  outside the band mean stop funding, wind down, deselect the route. Fraud
  is not adjudicated; it is defunded.
- Because payout follows claims, sellers have no incentive to under-report,
  and over-reporting is capped by the band and then by defunding.

## 5. Exposure arithmetic

The buyer's worst case is always computable:

```
max_exposure = runway_increment × concurrent_sessions_with_that_route
```

The runway increment is therefore the buyer's **risk dial**, and it is
per-route economics, not protocol: a staked, long-history orchestrator can
be granted larger increments and wider bands (fewer top-ups, less control
chatter); a new route gets small increments and tight bands. The offering's
`runway_increment_units` is advisory sizing from the seller; the buyer
always owns the actual number, because the buyer owns the exposure.

The seller's mirror-image worst case is one increment of work delivered
beyond exhausted funding, bounded by the same mechanics: the broker's
fail-closed debit loop, heartbeat enforcement, and the funded ceiling on
streaming jobs.

## 6. Verifiability hooks

Descriptor schemas SHOULD place a cheaply probeable coordinate in their
public part — a status endpoint, an advancing playlist, an attachable
health surface. This gives the buyer *product verification* (is the thing I
am funding real and serving?) as a complement to edge metering, without any
process metering. Schemas that omit a hook force buyers to rely on claims
plus attachment alone; that is a route-selection signal in itself.

## 7. Threats and their answers

| Threat | Answer |
|---|---|
| Seller inflates usage claims | Band tripwire vs edge signal; exposure capped at one increment; defund + deselect. |
| Seller meters but does not serve ("dark runtime") | Verifiability probes, customer-side failure signals, heartbeat-visible state; same bounded exposure. |
| Seller serves past funding to manufacture debt | Nothing owed: the protocol recognizes no debt beyond funded runway; fail-closed is the seller's own tool, declining to use it is the seller's own loss. |
| Buyer consumes without paying | Broker fail-closed enforcement: debit-before-serve on jobs (funded ceiling), runway + lease + heartbeat winddown on sessions. |
| Buyer replays / double-settles | Payee-daemon idempotency on `(sender, work_id, debit_seq)`; protocol-level idempotent opens and event dedup. |
| Either side disputes "what really happened" | Nobody arbitrates. Both sides kept their own meter precisely so that disagreement resolves by defunding/deselection, not by proof. |
| Customer disputes the gateway's bill | Off-protocol by design: the gateway bills from its own edge records, which it owns end to end and can show its customer. |

## 8. What this replaces

`streaming-workload-pattern.md`'s scattered trust assumptions (broker-side
meter as the ledger of record, delivered-event outbox as a gateway
guarantee) are superseded by this doc plus the `paid-session/v1` spec. The
durable-outbox and replay requirements it stated survive — as the seller's
own durability obligations in the session protocol — but the framing "the
broker meters for the gateway" does not. That doc gets a status note at the
purge, not a rewrite.
