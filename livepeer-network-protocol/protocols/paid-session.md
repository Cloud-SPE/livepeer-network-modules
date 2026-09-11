---
title: Paid Session Protocol
version: 1.3.0-draft
status: draft
last_updated: 2026-09-11
---

# Paid Session Protocol

`paid-session/v1` governs long-lived, metered workloads such as live video,
rooms, realtime inference, and interactive media. It is workload-agnostic: the
runner descriptor defines how the caller reaches the workload; this protocol
defines authority, accounting, lifecycle, replay, and recovery.

## 1. Roles and invariants

- The provider or clearinghouse is the payer and wholesale-account owner.
- The orchestrator is the payee and runs the broker, payment daemon, and runner.
- The workload caller may be distinct from the payer. A caller proof delegates
  invocation without delegating ticket minting or account ownership.
- Tickets add expected value to the stable payer-payee account. They never
  authorize a workload.
- Every open and refill carries a signed, single-purpose
  `Livepeer-Authorization`. `Livepeer-Payment` is optional shortfall funding.
- Admission atomically reserves the authorization maximum. Usage debits the
  reservation; settlement releases unused value to the aggregate account.
- No offer flag negotiates this behavior and no payment-only compatibility path
  exists.

The authorization and account wire objects are defined by
[`wholesale-account.md`](./wholesale-account.md). Header encoding and stable
errors are defined by [`livepeer-headers.md`](../headers/livepeer-headers.md).

## 2. Offering contract

An offering uses `protocol: paid-session/v1` and declares one canonical work
unit and price. `session_policy` defines attachment, refill, lease, runway, and
heartbeat behavior. Account authorization is implicit for every paid offering;
operators MUST NOT advertise `extra.features.wholesale_accounts` as a feature
switch.

The accepted authorization price MUST exactly match the selected offering:
capability, offering, work-unit name, price amount, denominator, and quote
reference. Price or route mismatch fails before account or runner effects.

## 3. Gateway-broker wire contract

### 3.1 Open

`POST /v1/session` requires:

- `Livepeer-Capability`
- `Livepeer-Offering`
- `Livepeer-Protocol: paid-session/v1`
- `Livepeer-Request-Id`
- `Livepeer-Authorization`
- JSON containing a non-empty, caller-issued `gateway_session_id` and
  workload-specific `session_params`

`Livepeer-Payment` MAY accompany the request only to cover aggregate account
shortfall. A request with payment but no authorization MUST return `401
authorization_required` before ticket processing, capacity acquisition, or
runner invocation.

The authorization MUST bind the exact request body digest, request id,
gateway-session id, protocol, capability, offering, broker URI, payer, payee,
chain, denomination, accepted price, maximum cumulative units, and maximum
debit. If `caller_public_key` is present, `Livepeer-Caller-Proof` MUST prove
possession for this exact authorization.

Successful admission:

1. verifies the envelope and optional caller proof;
2. atomically funds any shortfall and reserves bounded runway;
3. acquires capacity and creates the runner session;
4. durably records authorization, quote binding, usage watermark, credential,
   runner binding, and recovery obligations;
5. returns `session_id`, `gateway_session_id`, `work_id`, session credential,
   sanitized runtime descriptor, control URLs, lease, and balance view.

`work_id` equals the active `authorization_id`; it is correlation state, not a
balance account. The stable economic owner is the payer-payee account.

### 3.2 Status

`GET /v1/session/{session_id}` requires the session credential and returns the
current public session view. At minimum it includes identifiers, state, lease,
cumulative usage, current authorization/account balance view, runtime public
descriptor, and output health:

- `output_state`: `waiting`, `producing`, `stalled`, or `unknown` for an older
  runner whose state has not yet been observed;
- optional `output_state_since`;
- optional sanitized `last_failure_code`.

Private runner material and grant secrets MUST NOT appear.

### 3.3 Authorization revision (refill)

`POST /v1/session/{session_id}/topup` requires the session credential,
`Livepeer-Request-Id`, and a successor `Livepeer-Authorization`.
`Livepeer-Payment` remains optional account funding.

The successor MUST bind the same payer, payee, session, route, price, chain, and
denomination; name the current `authorization_id` in
`predecessor_authorization_id`; increment revision; and MUST NOT reduce the
cumulative unit or debit cap. The broker atomically admits the successor before
making it current. A bounded-refill offering returns `refill_refused`.

Replay is checked before current-predecessor and terminal checks. The same
request id plus identical authorization and payment returns the recorded result
without funding or reserving twice. The same request id with different bytes
returns `request_id_reuse`.

Ticket-session rotation and `Livepeer-Rebind-From` are not part of this
contract. Funding generations are internal to the stable account and cannot
change workload identity.

### 3.4 End

`POST /v1/session/{session_id}/end` requires the session credential. It is
idempotent. The broker terminates the runner, settles the active authorization
at actual cumulative usage, releases unused reservation and capacity, writes a
terminal state, and emits signed settlement evidence.

### 3.5 Settlement lookup

`GET /v1/settlement/{id}` resolves `session_id`, the unique
`gateway_session_id`, or an unambiguous retained `work_id`. Ambiguous identifiers
return `409 ambiguous_identifier`; the broker MUST NOT choose an arbitrary
session.

The signed settlement includes the unchanged accepted `QuoteRef`, session and
gateway identifiers, authorization id, cumulative actual/billed units, billed,
reserved, released, and account-funding values, account version, terminal
state/reason, and output-health details when present.

## 4. Runner descriptor and grants

The runner creates the workload only after admission. Its descriptor schema is
selected by the offering and validated strictly with unknown-field rejection
and a bounded payload size. The broker exposes only the schema-defined public
projection. Grant secrets are delivered once on the successful open response,
may be replayed only while the original open remains replayable, and are erased
on winddown. Restarts MUST NOT mint replacement grants.

## 5. Events and metering

Runner callbacks are authenticated and ordered by `(event_id, sequence)`.
Events are committed atomically with their usage and output-health watermark.
Duplicate events return the prior outcome; sequence regression, unit mismatch,
and malformed health details change neither usage nor health.

For a cumulative usage total `u`, the broker calls `AdvanceAuthorization` with
the same cumulative total and an idempotent advance sequence. Billing uses one
cumulative ceiling:

`bill(u) = ceil(price_amount_wei * u / per_units)`

The delta from the last committed cumulative bill is debited. When the
authorization cap is reached, the broker records the bounded total and winds
down with `authorization_exhausted`; it never bills beyond the signed cap.

Recognized output events include `session.ladder.restart` and
`session.output.stalled`. `details.output_state` and safe
`details.last_failure_code` persist with the same event watermark. A stalled
event is not evidence of healthy output. Persistent stall or
`session.failed`/`output_failed` winds the session down; `output_failed` remains
the terminal reason rather than being rewritten to `runner_failed`.

## 6. Balance and runway

The public balance object is derived from the authorization reservation and
account state, not a ticket-session balance. It reports sufficient information
to distinguish account availability, current reserved value, cumulative billed
value, cap remaining, and whether the next revision will be refused.

Fixed-lease offerings may end independently of funding. Funding-tracking
offerings clamp reserved runway to policy bounds and the signed authorization
maximum. A payment can increase aggregate account availability but cannot
extend authority without a valid successor authorization.

## 7. Idempotency and uncertain settlement

Open is idempotent on `Livepeer-Request-Id` plus the exact body,
authorization, and optional payment. A concurrent identical open either replays
the recorded result or returns `open_in_flight`; different content returns
`request_id_reuse`. No duplicate account reservation or runner session may be
created.

Advance and settlement operations are idempotent. If the daemon response is
uncertain, the broker retains the reservation and retries the same operation;
it MUST NOT write the debit off, release the reservation, or create a second
authorization to guess the result.

## 8. Control WebSocket binding

`control.events_ws` is an optional push mirror; HTTP remains authoritative.
Attachment requires the session credential. Broker-to-caller frames include
usage, balance, output health, state, and terminal events.

The gateway-to-broker `session.topup` body requires:

```json
{
  "request_id": "...",
  "authorization_header": "<base64 SpendAuthorization>",
  "payment_header": "<optional base64 Payment>",
  "caller_proof": "<optional proof>"
}
```

The authorization request digest is SHA-256 of the empty HTTP-equivalent refill
body. The same scope, price, predecessor, proof, replay, and bounded-refill rules
as §3.3 apply. Payment-only frames return `authorization_required`. Every
gateway-initiated frame receives an ack or stable error frame.

## 9. Durability, recovery, and cutover

Nonterminal records persist the active authorization id and caps, payer, quote
reference, usage/billing/event watermarks, session and runner bindings,
credentials in sealed/hashed form, output health, and settlement obligations.
Open reservations persist enough state to undo runner creation and release an
authorization after a crash.

On restart the broker verifies that the runner session and active authorization
still exist. If both survive, processing resumes from durable watermarks. If
the authorization is absent or unverifiable, the broker marks the payment
obligation unavailable, terminates the runner, releases capacity, and reaches
`recovery_failed`; it MUST NOT keep serving unbillable work.

This release is a hard cut. Before deployment, operators MUST stop admission,
drain every payment-only nonterminal session and paid open reservation, and
verify none remain. Broker startup refuses such records. Terminal legacy
records may remain queryable through retention but cannot be resumed.

## 10. Minimum conformance

A conforming implementation proves at least:

- payment-only open and refill fail before payment or runner effects;
- authorization scope, body digest, caller proof, route, and price fail closed;
- open and revision replay are exactly-once;
- usage advances and settlement use cumulative ceiling arithmetic;
- unused reservation returns to the stable account;
- restart preserves authorization/account state and watermarks;
- lost authorization state terminates the runner fail closed;
- quote binding survives restart and appears unchanged in every settlement;
- output health is atomic with event ordering and persistent stall terminates;
- control-WS authorization revision mirrors HTTP behavior.

## Changelog

| Version | Date | Change |
|---|---|---|
| 1.3.0-draft | 2026-09-11 | Makes wholesale accounts and single-purpose authorization the only paid-session accounting path; replaces ticket-session top-up/rebind with predecessor-bound authorization revisions; specifies account-only recovery, WebSocket revision frames, and the coordinated hard cut. |
| 1.2.0-draft | 2026-09-09 | Added accepted quote persistence and output-health settlement fields. |
| 1.1.0-draft | 2026-09-08 | Added standardized live output-health events and terminal behavior. |
