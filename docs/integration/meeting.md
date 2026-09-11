# Meetings — what to change

Target: `paid-session/v1`. Two of these are things you already found and
fixed; they are here so the next person does not rediscover them.

## 1. Capability and offering go in HEADERS

`Livepeer-Capability` and `Livepeer-Offering`, not the body. A body-only
open gets `404 capability_not_served`.

## 2. `gateway_session_id` is REQUIRED

```json
{ "gateway_session_id": "<uuid>", "session_params": { ... } }
```

An open that omits it, or sends it empty or whitespace, is now refused
with `invalid_request` (400). It used to be accepted silently, which
produced settlements carrying an empty value for the only identifier
their consumer issues itself.

It must be globally unique across the broker's retained sessions —
a second open claiming a live one gets `gateway_session_id_reuse` (409).
Generate it per session; do not derive it from anything stable per room
or per tenant.

## 3. Refill is an authorization revision

Every refill carries a successor `Livepeer-Authorization` naming the current
`authorization_id` as its predecessor and increasing the revision. An optional
`Livepeer-Payment` only replenishes aggregate account float. Ticket recipient
rotation is invisible to this session and never causes a workload rebind.

Lost local state? `GET /v1/session/{id}` returns the current authorization id
and cumulative usage. Do not mint a successor until the predecessor outcome is
known.

## 4. Settlement lookup

`GET /v1/settlement/{id}` resolves your `gateway_session_id`, the broker
`session_id`, or its single-purpose authorization id. Prefer your own
`gateway_session_id`; it remains stable across authorization revisions.

## 5. Lease and metering

- A successful authorization revision can extend the signed cumulative cap;
  the broker still reserves only bounded runway.
- `Livepeer-Request-Id` is required on top-ups and is the idempotency
  key. A replayed top-up returns the recorded outcome rather than
  funding twice.
- Usage events come from your runner over its callback, with
  `event_id` and monotonic `sequence`. Duplicates and reorders are safe;
  a unit-name mismatch advances nothing.

## 6. What the pilot stack gives you, and what it does not

The stack runs a **stub** SFU runner: it answers create/status/terminate
and returns an `sfu-room/v1` descriptor, so you can exercise open,
lease, top-up, end and settlement today.

It does not meter, because it emits no usage events. Full session
metering needs your real runtime posting them — which is what LOC's
pilot item 6 is asking for. Attach your own runtime as a runner (it
declares its own session paths and metering) and the rest of the path is
unchanged; the offer, its price and its session policy do not move.
