---
title: Paid Session Protocol
version: 1.4.0-draft
status: draft
last_updated: 2026-09-13
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

If step 3 receives runner `429 capacity_reached`, no runner session exists but
the authorization from step 2 has already been admitted. The broker MUST
settle it at zero cumulative units, release its reservation, persist signed
terminal settlement evidence, and return public `503 capacity_exhausted` with
`Livepeer-Backoff`. A retry replays that terminal outcome and cannot create a
session. A broker that rejects capacity before step 2 instead records signed
`NOT_ADMITTED` evidence; one request can never acquire both records.

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

Successor limits are cumulative, including usage billed under the predecessor.
The broker MUST bound the requested reservation by the successor's remaining
debit allowance after reconciling receiver billing and pending usage. HTTP and
control WebSocket revisions follow the same rule. For 180 authorized units,
74 billed units and a requested runway of 120 at 10^12 wei/unit, reserve
106 × 10^12 wei. A reservation is not an additional charge.

The broker persists revision identity, exact authorization/payment bytes,
fingerprint, derived reservation and promised lease before admission. Recovery
may correct an oversized derived reservation while preserving those identities
and bytes. An already-admitted successor wins over a replayed reservation;
recovery must adopt the receiver's actual result and inherited billing.
Authority replacement and the replayable response commit atomically.

On winddown or definitive refusal, `CancelAuthorizationAdmission` on the
receiver's trusted socket atomically fences an absent admission or returns the
existing authorization unchanged, checked against SHA-256 of its signed bytes.
A `NotFound` lookup alone cannot authorize abandoning an in-flight admission.
A durable cancellation or expired-unused successor produces a replayable
`refill_refused` result and retains the predecessor. An accepted successor must
be reconciled and settled as the current authority. Receiver outages retain
the intent and financial obligations; background retry schedules survive
restart. Signed terminal settlement is withheld until financial closure is
confirmed. Usage and billed totals never reset at a revision boundary.
`GetSpendAuthorization` reports a fenced identity as
`SPEND_AUTHORIZATION_CANCELED_UNUSED`, distinct from an unknown authorization;
regional drain checks treat that state as terminal with no usage.

Expected receiver admission refusals use gRPC `FailedPrecondition` with
`google.rpc.ErrorInfo.domain = "payments.livepeer.org"`. Defined reasons are
`RESERVATION_EXCEEDS_REMAINING_DEBIT`, `REVISION_PREDECESSOR_INVALID`,
`REVISION_LIMITS_BELOW_USAGE`, `AUTHORIZATION_EXPIRED_UNUSED`,
`AUTHORIZATION_ADMISSION_CANCELED`, `AUTHORIZATION_NOT_ACTIVE`,
`AUTHORIZATION_NOT_ADMITTED`, `AUTHORIZATION_STATE_INVALID`, and
`INSUFFICIENT_WHOLESALE_CREDIT`. `AUTHORIZATION_NOT_ACTIVE` is retryable;
the broker reconciles/fences other refusals before discarding an intent.
An `Internal` error or transport timeout is not proof of non-admission.

#### Revision decisions and evidence

After a revision intent is persisted, HTTP responses, control-WebSocket acks or
errors, and session status expose a `revision` object. Its `outcome` is `pending`,
`admitted`, or `refused`. It includes `reason`, `stage`, `receiver_code`, broker
`session_id`, `gateway_session_id`, `request_id`, `authorization_id`,
`predecessor_authorization_id`, `revision`, `observed_at`, and, while deferred,
`next_retry_at`. Optional decimal-wei diagnostics are `requested_reservation_wei`,
`reservation_wei`, `receiver_billed_wei`, and `max_debit_wei`; unit diagnostics are
`receiver_actual_units` and `max_total_units`. These are observations, not bills.
Preflight validation errors have no durable receiver decision or signed proof.

Unresolved admission returns HTTP 503, `Livepeer-Error: revision_pending`, and
`Retry-After: 1`; its WebSocket error has `code: revision_pending`. Definitive
refusal returns HTTP 409 / WebSocket `refill_refused`. Both errors contain
`revision`. Clients retry identical bytes and request ID or query the outcome;
they MUST NOT infer non-admission from the error code alone. Successful replies
carry the admitted decision. A completed retry returns the original decision.
Session status exposes the most recent decision, not a history.

`stage` identifies `admission`, `predecessor_lookup`, `reservation`,
`reconciliation`, or `lifecycle`. Receiver validation also supplies bounded
`google.rpc.ErrorInfo` reasons for malformed/signature/scope/time/price/limit
errors, funding refusal and frozen source admission. The broker understands an
explicit allowlist; unknown receiver reasons remain pending with
`ADMISSION_OUTCOME_UNKNOWN`, and transport failures use `RECEIVER_UNAVAILABLE`.
Neither gRPC code alone nor internal error text authorizes cancellation. A known
refusal's original cause is saved before cancellation and survives cancellation
response loss and restart. An admission already accepted by the receiver wins
even if its response was lost; the final outcome is then `admitted`.
Its reason may retain the earlier failure for diagnosis; `outcome` is the
authority decision, and reason is never an accounting instruction.

`GET /v1/session/{id}/revisions/{request_id}` is an independent evidence read.
`id` is the broker session ID or unique retained gateway session ID; broker IDs
take precedence. Like settlement lookup, possession of the unguessable identifier
allows reading evidence without a gateway credential. Consumers verify the
delegated broker signature and their own issued scope.

| Result | HTTP | Body / header |
|---|---|---|
| Pending | 202 | `outcome: pending`, `revision`, `Retry-After: 1`; no signed evidence |
| Admitted or refused with proof | 200 | `revision`, `revision_evidence`; identical base64 envelope in `Livepeer-Revision-Evidence` |
| Historical outcome without retained proof | 409 | `revision_evidence_unavailable`; no invented proof |
| Signing/store temporarily unavailable | 503 | `revision_evidence_unavailable` or `revision_pending` |
| Unknown or evicted outcome | 404 | No assertion of non-admission |

The payload is [`SessionRevisionRecord`](../proto/livepeer/payments/v1/session_revision.proto),
using the settlement JCS/EIP-191 envelope and delegated settlement key. Its
`evidence_domain` is `livepeer-session-revision/v1`; `protocol` is
`paid-session/v1`. It binds settlement domain, broker URI, broker and gateway
session IDs, request ID, payer/payee, predecessor/successor IDs, revision,
SHA-256 of the **exact signed authorization wire bytes**, accepted quote, caps,
outcome, basis and observation time. Consumers MUST compare these with their own
grant and verify the signature; the unsigned `revision` diagnostics are not proof.
Claimed fields from an invalid authorization do not attest its validity.

Only `ADMITTED` or `NOT_ADMITTED` is signed. `NOT_ADMITTED` requires
`receiver_fenced` or `expired_unused` evidence. It applies only to that successor,
not the whole session. Persisted final envelopes replay verbatim, including after
restart or signing-key rotation, while retained. Generic non-admission lookup for
a known revision returns `409 revision_evidence_required`, including after
outcome eviction. Minimal revision request tombstones outlive outcome retention.
Upgraded brokers refuse generic paid-session assertions predating their revision
coverage horizon (`coverage_gap`), since already-evicted old refills cannot be
reconstructed safely.

#### Meaning of canceled_unused

`SPEND_AUTHORIZATION_CANCELED_UNUSED` is a durable receiver fence, committed in
the same ledger transaction boundary used by admission. It survives restart and
has no reversal or expiry operation. Future admission of the same payer and
authorization ID is rejected, even with different bytes; conflicting bytes also
fail the fingerprint check. An existing admitted authorization is returned
unchanged instead of being canceled. The fence records no usage and does not
refund or settle any predecessor usage.

The trusted receiver RPC checks the receiver's durable `settlement_domain_id`,
uses the configured payee, and keys the fence by payer and authorization ID with
SHA-256 of the signed authorization. The broker's signed revision evidence binds
those identities, domain, and hash for LOC; a bare status enum over the receiver
socket is not a signed authorization-specific envelope. This guarantee assumes
the same durable ledger is retained; destructive rollback to a pre-fence backup
does not preserve it.

The [revision evidence fixture](../conformance/fixtures/session-revision-evidence.json)
contains public test-key signed refusal and terminal cap-overshoot envelopes,
expected repeated-close bytes, and unsigned pending semantics. It is synthetic
contract data, not a spendable authorization or production evidence.

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

`settlement_seq` MUST be positive and monotonic within the broker session, and
MUST NOT reset on an authorization revision. It is independent of the receiver
authorization's usage/settlement sequence. Brokers atomically freeze the terminal
payload and next session sequence with the terminal state before publishing it;
repeated lookup or close returns the same sequence, timestamp, payload and signed
envelope, including after restart. A pre-fix terminal record missing a frozen
payload is upgraded once, using the next broker sequence, never a receiver
sequence. LOC treats an exact duplicate accepted terminal envelope as an
idempotent close; conflicting evidence at the same sequence remains a replay
violation. Sequence zero remains invalid.

`claimed_units` records runner-observed work. `debited_units`, `actual_units` and
`billed_units` record the receiver-confirmed billable quantity. A coarse usage
report can cross an authorization cap: claimed 124, all three billed quantities
120, and `termination_reason: authorization_exhausted` is an intended bounded
outcome. The broker reports `claim_debit_gap: true` and
`claim_debit_gap_reason: authorization_cap`. Excess claimed work is seller risk;
it does not increase payer liability. LOC retains signature, identity, replay,
price, cap and cumulative-accounting checks and bills only the verified 120
(120 × 10^12 wei at 10^12 wei/unit). Other gaps are labeled
`unreconciled_claim` and require investigation; a diagnostic label alone never
proves correct accounting.

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
the authorization is absent or unverifiable, the broker terminates the runner
and records `recovery_failed`; it MUST NOT keep serving unbillable work. Missing
receiver state is not evidence of settlement: the session remains
`winding_down`, retains its financial obligations and capacity, and withholds a
signed terminal result until the receiver ledger has been reconciled. A lost
settlement response is recovered by replaying its receiver-confirmed cumulative
units and sequence, not by fabricating closure or resetting the bill.

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
| 1.4.0-draft | 2026-09-13 | Defines session runner capacity refusal after authorization admission as public 503 plus a durable signed zero-use settlement, distinct from pre-admission evidence. |
| 1.3.0-draft | 2026-09-11 | Makes wholesale accounts and single-purpose authorization the only paid-session accounting path; replaces ticket-session top-up/rebind with predecessor-bound authorization revisions; specifies account-only recovery, WebSocket revision frames, and the coordinated hard cut. |
| 1.2.0-draft | 2026-09-09 | Added accepted quote persistence and output-health settlement fields. |
| 1.1.0-draft | 2026-09-08 | Added standardized live output-health events and terminal behavior. |
