# vtuber + daydream — what to change

Target: `paid-session/v1`. You two get one guide because you solved the
same problem in opposite directions, and the fix is the same shape: the
broker becomes the session authority you were either pinned to or
emulating.

The analysis is in
`docs/references/2026-08-19-gateway-migration-packet-paid-session.md`.
This is the operational half — what to call, and what comes back.

## 1. There is no `vtuber-session@v0` or `daydream-scope@v0`

Workload identity lives in the **runtime-descriptor schema**, not a
protocol name. Both of you speak the same protocol, `paid-session/v1`,
and differ only in the descriptor the runner emits:

| You | Offering on the pilot stack | Descriptor schema |
|---|---|---|
| vtuber | `livepeer:vtuber/session` / `default` | `trickle-egress/v1` |
| daydream | `livepeer:daydream/session` / `default` | `scope-passthrough/v1` |

daydream: `session-control-external-media@v0` is gone from every
registry. It still works only because the broker you talk to still
answers, not because anything guarantees it will.

## 2. Capability and offering go in HEADERS

`Livepeer-Capability` and `Livepeer-Offering`, not the body. A body-only
open gets `404 capability_not_served`. `Livepeer-Request-Id` is required
too — it is what binds a settlement to your durable job.

## 3. `gateway_session_id` is REQUIRED and globally unique

```json
{ "gateway_session_id": "<uuid>", "session_params": { ... } }
```

Omitted, empty, or whitespace is refused with `invalid_request` (400). A
collision with a live session is refused with `gateway_session_id_reuse`
(409). Uniqueness is global, not per-tenant, so derive it from something
with real entropy — a UUID. A counter or a short slug will collide across
tenants and the loser gets a 409 at open.

`session_params` is where workload config goes. daydream: this replaces
smuggling it through `/api/v1/*` after the fact.

## 4. What an open actually returns

Captured from the pilot stack against Arbitrum One, not written from the
spec. vtuber:

```json
{
  "session_id": "sess_a1f5da63-…",
  "work_id": "b910a579328a…",
  "state": "active",
  "credential": "sc_bc3a427116d928a3b44c9478e93f89de",
  "lease": { "expires_at": "2026-08-22T21:26:51Z" },
  "control": {
    "status_url": "http://…/v1/session/sess_a1f5da63-…",
    "topup_url":  "http://…/v1/session/sess_a1f5da63-…/topup",
    "end_url":    "http://…/v1/session/sess_a1f5da63-…/end",
    "events_ws":  "ws://…/v1/session/sess_a1f5da63-…/ws"
  },
  "balance": {
    "unit": "participant-seconds",
    "claimed_units": 0, "debited_units": 0,
    "runway_units": 19531249999980,
    "status": "ok", "will_refuse_next_refill": false
  },
  "runtime": {
    "schema": "trickle-egress/v1",
    "public": {
      "control_url": "wss://…/control/rns_d8ed27253528",
      "preview_url": "https://…/preview/rns_d8ed27253528",
      "status_url":  "https://…/status/rns_d8ed27253528"
    },
    "grants": [{
      "id": "control-attach",
      "operations": ["control-attach"],
      "secret": "vt_rns_d8ed27253528",
      "expires_at": "2030-01-01T00:00:00Z"
    }]
  }
}
```

daydream is identical except `runtime`:

```json
"runtime": {
  "schema": "scope-passthrough/v1",
  "public": {
    "scope_url":  "https://…/scope/rns_dabbb78fa137",
    "status_url": "https://…/status/rns_dabbb78fa137"
  },
  "grants": [{
    "id": "scope-api-access",
    "operations": ["scope-api-access"],
    "secret": "dd_rns_dabbb78fa137",
    "expires_at": "2030-01-01T00:00:00Z"
  }]
}
```

Everything you were synthesizing is in that one response: the credential,
the lease, the balance, the control URLs, and the runtime coordinates.

**`public` is a closed key set.** Only the keys listed in
`livepeer-network-protocol/descriptors/` may appear there. The grant
`secret` is never public — it is the bearer for `control_url` /
`scope_url`, scoped to this session, and leak fixtures exist to catch it
appearing anywhere it should not.

## 5. What you can delete

vtuber, this is the subtractive one: `synthesizeStaticRoute` and its
fabricated `0x0000…` address, `"0"` price and self-computed quote id; the
bearer vault; the quote-column denormalization; the private control
protocol. You keep your product surface — api keys, concurrency caps, the
customer control relay, persona/VRM params. Your runner keeps its usage
measurement; it reports cumulative totals on the standard event channel
instead of a bespoke callback, and retries are safe by contract because
exactly-once debit is the broker's problem.

Both of you: the payer-side call moves off the legacy
`faceValue + recipient + capability + offering` shape. vtuber already did
that migration — plan `0003-daemon-contract-alignment` is the reference.

## 6. Rotation needs nothing from the socket

If the payee rotates its recipient rand, a stale payment is refused with
`recipient_rotated`. Declare the last `work_id` you held and rebind —
read it back from `GET /v1/session/{id}` if you lost it. You do not need
to have been listening on the events socket.

## 7. Settlement lookup

`GET /v1/settlement/{id}` resolves your `gateway_session_id`, the broker
`session_id`, or any `work_id` the session has held. Prefer your own id:
it is the only one you issue yourself. An ambiguous key answers
`ambiguous_identifier` (409) and names one that resolves, rather than
guessing.

## 8. The pilot stack: what it gives you, and what it does not

Both offerings are live and open real sessions against Arbitrum One —
verified end to end, HTTP 201 with the descriptors above.

What it does **not** do is meter. The stub runner emits no usage events,
so `debited_units` stays 0 and no billing happens. That needs your real
runtime posting cumulative totals. Point the offering at it with
`VTUBER_RUNNER_URL` or `DAYDREAM_RUNNER_URL` and the rest of the path is
unchanged:

```
VTUBER_RUNNER_URL=http://your-runner:9500 ./up.sh
```

Your runner must answer create with `runner_session_id` and a `runtime`
object — **not** `session_id` and `descriptor`. The broker fails the open
closed when either is missing, and the operator sees `backend_unavailable`,
which reads as an outage rather than a field-name mismatch. Normative in
`paid-session.md` §7.1.
