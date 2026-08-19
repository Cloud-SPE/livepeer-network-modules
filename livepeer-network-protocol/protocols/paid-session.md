---
spec_name: paid-session
version: 1.0.0-draft
status: draft
last_updated: 2026-08-18
---

# Protocol: `paid-session/v1`

A long-lived paid engagement: the gateway opens a session with the broker,
the broker binds a runner-owned runtime and becomes the durable authority for
that session's payment, lease, and lifecycle, and usage flows back as claims
until the session ends. Media and workload data never transit the broker.

`paid-session/v1` replaces every session-family interaction mode
(`ws-realtime@v0`, `rtmp-ingress-hls-egress@v0`, `session-control-plus-media@v0`,
`live-session-remote-runner@v0`, `live-session-gateway-ingest@v0`). What the
runtime *is* — an SFU room, an RTMP ingest, a generative-video scope — lives
entirely in the session's [runtime descriptor](./runtime-descriptor.md)
schema, never in the protocol.

Durability is not an extension. A broker that cannot survive its own restart
without orphaning runners and stranding customer balances does not implement
this protocol.

The key words MUST, MUST NOT, SHOULD, and MAY are to be interpreted as in
RFC 2119.

## 1. Roles and trust context

Per the trust-model doc, both sides meter and neither trusts the other's
meter:

- The **broker** is the seller's edge: it validates payment before binding a
  runtime, converts the runner's cumulative usage claims into debits, and
  fails closed — no funded runway, no running runtime.
- The **gateway** is the buyer's edge: it holds the session's admission
  grants, meters attach/join/duration at its own edge, bills its customers
  from that signal, and treats broker/runner usage strictly as claims for
  runway accounting and divergence detection.
- The **runner** owns the runtime and is the source of usage claims. It never
  talks to the payment layer.

The exposure bound is structural: a gateway funds runway in increments, so a
dishonest counterparty is worth at most the outstanding increment before the
tolerance band trips and funding stops.

## 2. Session lifecycle

```
open ──► active ──► winding_down ──► ended
  │         │                          ▲
  └─────────┴──────── failed ──────────┘
```

- `active` — runtime bound, lease current, claims flowing.
- `winding_down` — terminal path entered (end requested, runway exhausted,
  heartbeat breach, refill refused); runtime termination and payment close in
  progress.
- `ended` / `failed` — terminal, with a stable `close_reason`. Terminal
  session records MUST remain queryable for an operator-configured retention
  window, then be evicted; the store is bounded.

## 3. Gateway ↔ broker wire shape

### 3.1 Open

`POST /v1/session`

Required headers: `Livepeer-Protocol: paid-session/v1`, `Livepeer-Capability`,
`Livepeer-Offering`, `Livepeer-Request-Id`, `Livepeer-Payment`.

Open is idempotent on `Livepeer-Request-Id` under the same contract as
`paid-job/v1` §4: a retried open converges on the original outcome and never
mints a second session or a second `work_id`.

Body: `{ "gateway_session_id": "<uuid>", "session_params": { … } }` —
`session_params` is opaque capability data, passed to the runner verbatim.

Response (success):

```json
{
  "session_id": "sess_01jx…",
  "work_id": "3f8a1dd7-…",
  "state": "active",
  "runtime": {
    "schema": "sfu-room/v1",
    "public": { … },
    "grants": [ … ]
  },
  "credential": "sc_9b2f…",
  "lease": { "expires_at": "2026-08-18T21:40:00Z" },
  "balance": { … §6 … },
  "control": {
    "status_url": "…/v1/session/sess_01jx…",
    "topup_url":  "…/v1/session/sess_01jx…/topup",
    "end_url":    "…/v1/session/sess_01jx…/end",
    "events_ws":  "wss://…/v1/session/sess_01jx…/ws"
  }
}
```

Rules:

- The broker MUST bind the runner session before returning success, and MUST
  fail closed (terminate any partial binding, close payment state) on any
  open-path failure after payment validation.
- `runtime` is the descriptor's sanitized public view plus grants, exactly as
  the framework spec requires: grants here, once, never again.
- `credential` is the resumable session credential (§4).

### 3.2 Status

`GET /v1/session/{session_id}` — authenticated by the session credential.

Returns `session_id`, `work_id`, `state`, the **identical** sanitized
`runtime.public` (no grants, ever), `lease`, `balance`, `usage` (cumulative
claimed units + unit name), timestamps, and `close_reason` when terminal.
Served from the broker's durable session record; the broker MUST NOT need a
synchronous runner round-trip to answer.

### 3.3 Top-up

`POST /v1/session/{session_id}/topup` — session credential plus a new
`Livepeer-Payment` envelope, idempotent on `Livepeer-Request-Id`.

Rules:

- Credits the **existing** payee-side payment session for the same `work_id`;
  a top-up MUST NOT create a new logical session.
- **A successful top-up extends the lease** (§5). Funding and lifetime move
  together; the response carries the new `lease` and `balance`.
- If the broker will refuse further refills (winddown pending, offering cap
  reached), it MUST have been advertising `will_refuse_next_refill: true` in
  `balance` beforehand, and MUST refuse with `refill_refused` and a stable
  reason — never accept payment it won't honor with lease.

### 3.4 End

`POST /v1/session/{session_id}/end` — session credential; idempotent.
`{ "reason": "gateway_close" }`. The broker MUST terminate the runner
session, close payment state, record the stable reason, and answer with the
terminal state. Repeat calls return the same terminal record.

## 4. The session credential

Open returns a bearer `credential` scoped to exactly this session. Status,
top-up, end, and the control-WS attach all require it. Properties:

- It survives broker restart (it authenticates against the durable record,
  stored hashed).
- It is the *gateway's* continuity object: a gateway that restarts needs only
  `(session_id, credential)` to resume full control — no re-derived quote
  context, no encrypted bearer vaults, no reattach protocol.
- It is not an admission grant: it controls the session, not the runtime.
  Customer-facing attach credentials come from descriptor grants and are the
  gateway's business.
- Presenting a valid session id with an invalid credential and presenting an
  unknown session id MUST produce indistinguishable `401` responses (no
  existence oracle). Comparison is constant-time against the stored hash.

## 5. Lease and heartbeat enforcement

Every session carries a lease (`expires_at`) and every offering declares a
heartbeat interval and missed-event threshold.

- **Lease**: the normative default is funding-tracking —
  `expires_at = now + (runway_units ÷ declared burn rate)`, capped by an
  operator-configured maximum, recomputed on open and on every successful
  top-up. An offering MAY declare a different lease policy in its manifest,
  which gateways can then read before opening; absent a declaration, the
  default applies. A session whose lease expires enters `winding_down` with
  reason `lease_expired` — but only after a grace window of one heartbeat
  interval past `expires_at`, so a top-up in flight at expiry never loses the
  race to the sweeper. Recording a timestamp is not enforcement; the broker
  MUST run the winddown.
- **Heartbeat**: the runner emits liveness events (§7). **Any accepted event
  refreshes liveness** — a `session.usage.tick` counts, so a runner already
  reporting usage inside `interval × missed_threshold` needs no separate
  `session.heartbeat` emitter. When the missed-event threshold is exceeded
  the broker MUST prevent an unmetered runtime from continuing: it MAY first query the runner's status path, then MUST
  idempotently terminate the runner session, close payment state, release
  held capacity, and record `heartbeat_lost`.
- Winddown from **any** trigger (end, lease expiry, heartbeat loss, runway
  exhaustion, refill refusal) runs the same idempotent terminal path with a
  stable machine-readable reason.
- **Precedence.** When more than one trigger is due at the same sweep,
  `heartbeat_lost` takes precedence over `lease_expired`: a dead runner is
  the more specific fact, and it points the operator at the runtime rather
  than at funding.

## 6. Balance and runway

The `balance` object is normative — it is the buyer's sole spec'd window into
seller-side funding state, and drives gateway top-up loops:

```json
{
  "status": "ok" | "low" | "exhausted",
  "claimed_units": 1284,
  "debited_units": 1284,
  "unit": "participant_minutes",
  "runway_units": 716,
  "runway_seconds_estimate": 430,
  "will_refuse_next_refill": false
}
```

`runway_seconds_estimate` is advisory; `runway_units` is arithmetic.
`will_refuse_next_refill` is the one-refill-ahead winddown warning: a gateway
seeing it can drain gracefully instead of discovering refusal mid-broadcast.

## 7. Runner ↔ broker contract

> Implementing a runner? [§11](#11-runner-obligations--the-implementers-checklist)
> collects every obligation this protocol places on you in one table,
> with the failure signature for each.

### 7.1 Backend paths are configuration

The broker reaches the runner via operator-configured paths declared with the
backend — create, status, terminate — with no default URL space imposed by
the protocol. (The old contract hard-coded `/v1/video/live/sessions`; nothing
video-shaped survives here.) The broker authenticates to the runner with
operator-configured credentials. On create, the broker passes
`session_params` verbatim plus its callback coordinates; the runner's create
response carries the runtime descriptor per the framework spec.

Callback coordinates handed to the runner MUST be derived from operator
configuration, never from inbound request `Host`/`X-Forwarded-Proto`
headers.

### 7.2 Runner events

`POST /v1/session/{session_id}/events`, authenticated by a per-session
callback token the broker minted at create time (stored hashed; verified
constant-time; auth checked before any session-existence disclosure —
unknown session and bad token are indistinguishable).

Event envelope:

```json
{
  "event_id": "evt_01jx…",
  "sequence": 17,
  "event_type": "session.usage.tick",
  "event_time": "2026-08-18T21:13:00Z",
  "state": "active",
  "usage": { "unit": "participant_minutes", "total": 60 },
  "close_reason": null,
  "details": { }
}
```

Required event types: `session.started`, `session.heartbeat`,
`session.usage.tick`, `session.failed`, `session.ended`.

Unknown fields in the event envelope are **tolerated and ignored** — the
broker is a tolerant reader here, so runners may carry their own
correlation fields (their session ids, a per-event delta) without
coordinating a spec change. Note that a per-event usage delta is ignored
by rule, not merely unread: cumulative `usage.total` is the only debit
basis.

Rules:

- `event_id` MUST be unique and non-empty; `sequence` MUST be positive and
  monotonic per session. Violations are protocol errors that advance
  nothing.
- `usage.total` is the cumulative claim and the debit basis; the broker
  derives deltas from it and MUST ignore any per-event delta field.
- `usage.unit` MUST equal the offering's declared work unit. A mismatch is a
  protocol error and MUST NOT advance event idempotency, sequence, or
  cumulative-usage progress.

### 7.3 Exactly-once debit

The core accounting invariant, stated observably:

> Event deduplication MUST be committed only together with durable debit
> progress. A transient payment failure followed by the runner's retry of the
> same event produces exactly one debit — never zero (acknowledged but
> uncharged) and never two.

A durable transaction, an outbox, or a payment-layer idempotency key are all
acceptable implementations. Consequences the fixtures pin: a failed debit
leaves the event unprocessed (the retry really retries); concurrent events
for one session never reuse a debit sequence; runway/insufficiency checks are
not lost on the retry path.

## 8. Control-WS binding (optional)

An offering MAY expose `control.events_ws`. Attaching requires the session
credential. Frames mirror the HTTP surface — broker→gateway:
`session.usage.tick` (cumulative claim), `session.balance` (the §6 object,
emitted at least on every `low`/`will_refuse_next_refill` transition),
`session.state`, `session.ended`; gateway→broker: `session.topup` (payment
envelope in-frame), `session.end`. Every gateway-initiated frame is
acknowledged; the HTTP surface remains available and authoritative — the WS
is a push optimization, and a gateway ignoring it loses nothing but latency.

## 9. Durability and recovery

### 9.1 What must survive

Active session authority survives broker restart. Persisted or securely
reconstructable, per session: all identifiers (session, gateway, runner,
work); capability, offering, backend binding; payment sender and payee
session; session credential and callback token (hashed); descriptor (public
and private per the framework's storage rules) and grant audit metadata (ids
and hashes, never secrets); last accepted `event_id` and `sequence`; claimed
and debited cumulative totals and the payment-layer debit sequence; lease;
state, `close_reason`, payment-close status; held capacity ownership.

The payment-layer debit sequence is called out deliberately: the payee
daemon durably remembers `(sender, work_id, debit_seq)`, so a broker that
resumes at sequence 1 has its real debits silently swallowed as replays.

### 9.2 Recovery outcomes

After restart, each non-terminal session reaches exactly one of two
outcomes:

1. **Rebind** — status, top-up, end, events, and the session credential all
   continue against the same `work_id`. Grants are not re-minted. The broker
   MAY query the runner's status path to reconcile state before accepting
   new events. Before accepting events the broker MUST re-assert the
   payee-side payment session for the work id; that call is idempotent, so
   it is a no-op when the payment layer kept its state and is what lets a
   session survive a payment daemon that restarted independently of the
   broker. If the payment session cannot be re-asserted, the session takes
   the terminal outcome below rather than accepting unbillable usage.
2. **Explicit terminal** — where safe rebinding is impossible, the broker
   runs the standard winddown (terminate runner, close payment, stable
   reason `recovery_failed`), never leaving limbo.

Forbidden outcomes, each with a conformance fixture: minting a second
`work_id` for the same session; accepting a stale callback against the wrong
session; silently skipping usage; double-debiting; and a runner left serving
without broker payment authority.

## 10. Conformance obligations

Executable fixtures every broker implementation MUST pass:

- open→claims→debit→topup(lease extended)→end happy path, descriptor public
  view byte-identical between open and status, grants absent from status;
- restart tests: status, top-up, end, and events succeed across a broker
  restart for an active session (rebind); recovery-impossible case reaches
  the explicit terminal outcome;
- fault injection: duplicate and reordered events are safe; transient debit
  failure charges exactly once after retry; unit mismatch rejected without
  advancing totals; empty `event_id` rejected;
- heartbeat breach forces idempotent runner and payment closure with a
  stable reason; lease expiry does the same; `will_refuse_next_refill` is
  advertised before any refill refusal;
- auth: unknown session vs bad credential/token indistinguishable on both
  the control surface and the events endpoint;
- a second open with the same `Livepeer-Request-Id` returns the original
  session, not a sibling.

## 11. Runner obligations — the implementer's checklist

Everything a runner must do, in one place. This section is normative but
**derivative**: each obligation is specified in full where it is
referenced, and where this checklist and a numbered section differ, the
numbered section governs. It exists because a runner author should not
have to reverse-engineer their contract from a protocol written mostly
from the broker's point of view.

The third column is the point of the exercise. A runner that gets one of
these wrong sees a specific failure, and knowing the signature in advance
is the difference between a diagnosable bug and an afternoon.

### Session creation

| Obligation | Specified in | Failure signature if violated |
|---|---|---|
| Respond to create with `runner_session_id` and a `runtime` descriptor | §7.1 | Open fails `502`; broker terminates any partial binding and closes payment state. |
| Emit exactly the four descriptor keys (`schema`, `public`, `private`, `grants`); no others | [descriptor §2](./runtime-descriptor.md) | Open rejected: `unknown top-level key at path $.<key>`. Unknown keys are a partition-bypass vector, not a compatibility affordance. |
| `schema` MUST equal the offering's declared `descriptor_schema` | descriptor §2.1 | Open rejected naming both tags. A runner that upgrades its schema without the offering being updated fails here, every time. |
| Keep the serialized `runtime` object within the size cap (16 KiB default) | descriptor §3 | Open rejected with the observed size and the cap. |
| Put nothing in `public` that the schema does not declare public | the schema's own doc | Not caught at runtime — the broker relays `public` verbatim. Caught by the schema's conformance fixtures, which is why they exist. |
| Never place long-lived credentials (API keys, TURN secrets) anywhere in the descriptor | descriptor §2.3 and the schema | Not caught at runtime. This one is on you. |
| The public part is immutable for the session's lifetime | descriptor §2.2 | No update mechanism exists in v1; coordinates that change mean the session ends and a new one opens. |

### Grants

| Obligation | Specified in | Failure signature if violated |
|---|---|---|
| Provide a grant for every operation the schema declares, with `id`, non-empty `operations`, `secret`, and `expires_at` | descriptor §2.4 | Open rejected: `grant[N] missing required field`. |
| Honour the grant secret on **every** operation the schema names (e.g. both `participant-token-mint` and `room-status` for `sfu-room/v1`) | the schema's own doc | Not caught by the broker — it is not in the grant's data path. The gateway simply cannot use the coordinate, which usually surfaces as a customer-visible failure. |
| Scope granted operations to this session only | descriptor §2.4 | Not broker-observable. A cross-session grant is a security defect in your runner. |
| Enforce `expires_at` and `max_uses`, and **refuse all grant operations once the session is terminal** — expiry is a backstop, not the lifetime | descriptor §2.4 rule 5 | Not broker-observable. A grant honoured after end is an unmetered runtime. |
| Do not expect grants to be re-issued: they are delivered once at open and never re-minted, including after a broker restart | descriptor §2.4 rules 1 and 3 | A runner that assumes re-issue will wait forever. |

### Usage events

| Obligation | Specified in | Failure signature if violated |
|---|---|---|
| Send `event_id` non-empty and unique per session | §7.2 | Protocol error; **nothing advances** — not idempotency, not sequence, not usage. |
| Send `sequence` positive and strictly monotonic per session | §7.2 | Same. An event at or below the committed watermark is treated as a duplicate and acknowledged without effect. |
| Report `usage.total` as a **cumulative** total, never a per-event delta | §7.2 | A runner sending deltas as totals under-reports permanently: the broker derives its debit from the cumulative figure. A per-event delta field is ignored *by rule*. |
| `usage.unit` MUST equal the offering's declared work unit | §7.2 | Protocol error advancing nothing — so a unit mismatch rejects **every** usage event for the session's lifetime. The single most common integration failure. |
| Never let cumulative usage go backwards | §7.2 | Protocol error (`usage_regression`), nothing advances. |
| Emit the required event types: `session.started`, `session.heartbeat`, `session.usage.tick`, `session.failed`, `session.ended` | §7.2 | A session that never reports is torn down as `heartbeat_lost`. |
| Emit *something* within `interval × missed_threshold` — any accepted event refreshes liveness, so a usage tick suffices | §5 | Torn down with `heartbeat_lost`: runner terminated, payment closed, capacity released. |
| Retry on `5xx`, with the same `event_id` and `sequence` | §7.3 | The broker's exactly-once contract depends on it: a transient debit failure leaves the event uncommitted precisely so your retry completes it. A runner that gives up loses that usage permanently. |
| Extra envelope fields are tolerated and ignored | §7.2 | None — carry your own correlation fields freely. |
| Authenticate every event with the callback token from create, at the callback URL from create | §7.1, §7.2 | `401`, indistinguishable from an unknown session (no existence oracle). |

### Termination

| Obligation | Specified in | Failure signature if violated |
|---|---|---|
| Make terminate idempotent; terminating an unknown or already-terminated session succeeds | §7.1 | A non-idempotent terminate turns every winddown retry into a spurious error and can leave the broker's state and yours disagreeing. |
| Actually stop serving on terminate — the broker treats it as authoritative | §5, §9.2 | Serving after terminate is unmetered work; the broker has already closed payment. |
| Answer the status path truthfully, including after termination | §7.1, §9.2 | Recovery uses it: reporting a session gone that you still serve strands it; reporting alive one you dropped delays the terminal outcome. |

### What the runner never does

- **Never talk to the payment layer.** The broker is the sole network-payment
  authority; a runner that contacts `payment-daemon` is outside the protocol.
- **Never set price.** Price is the operator's declaration; a runner that
  reports monetary value rather than work units is misusing the contract.
- **Never treat its usage claims as billing truth for the buyer.** They are
  the seller's meter (see the dual-meter trust model). The gateway bills
  its own customers from its own edge.

## Changelog

| Version | Date | Change |
|---|---|---|
| 1.0.1-draft | 2026-08-19 | Add §11, the consolidated runner-obligations checklist, with the failure signature for each violation. Derivative and non-normative where it conflicts with a numbered section. |
| 1.0.0-draft | 2026-08-18 | Initial protocol. Replaces the five session-family modes; durable authority, exactly-once debit, lease/heartbeat enforcement, session credential, and the balance object become normative. Absorbs meeting-handoff requirements B1–B5 and A4. |
