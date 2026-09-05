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

## 3. Rotation: you need nothing from the socket

On `409 recipient_rotated`, declare the last `work_id` you held as
`Livepeer-Rebind-From` on an ordinary top-up. That is by definition the
predecessor: the rotation happened payee-side and the broker learns of it
from the same refusal you did.

Lost your state? `GET /v1/session/{id}` returns the session's current
`work_id`. The `session.rebound` control message is an optimisation, not
a precondition — a polling gateway has everything the rebind needs.

## 4. Settlement lookup

`GET /v1/settlement/{id}` resolves your `gateway_session_id`, the broker
`session_id`, or any `work_id` the session has held. Prefer your own id:
a `work_id` can cover several sessions, and an ambiguous one answers
`ambiguous_identifier` (409) rather than guessing.

## 5. Lease and metering

- A successful top-up extends the lease; funding and lifetime move
  together.
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
