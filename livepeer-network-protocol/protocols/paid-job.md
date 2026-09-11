---
spec_name: paid-job
version: 1.2.0-draft
status: draft
last_updated: 2026-09-11
---

# Protocol: `paid-job/v1`

One paid exchange: a caller submits one workload with a single-purpose spend
authorization, the broker admits it against a stable wholesale account, a
runner produces one response, and the broker settles measured usage. There is
no paid-session lease or control plane.

`paid-job/v1` supports unary, streaming, and multipart transports without
creating separate payment semantics for each workload shape.

The key words MUST, MUST NOT, SHOULD, and MAY are interpreted as in RFC 2119.

## 1. Economic model

Every exchange follows [wholesale-account.md](./wholesale-account.md).
Probabilistic tickets only fund the stable `(chain, payer, payee,
denomination)` account. A ticket, payment envelope, recipient-random
generation, or residual balance never authorizes a workload.

Every job requires a signed `SpendAuthorization` scoped to exactly one
request. The broker MUST atomically reserve its maximum before invoking a
runner and MUST settle actual measured units afterward. Unused reservation
returns to the stable account.

The wholesale payer may serve many retail customers. Retail identity, pricing,
credits, and billing remain the provider's concern and MUST NOT alter the
wholesale account key. The provider reconciles wholesale settlement evidence
to its own customer ledger using its request ID.

## 2. Transport

An offering declares the transports it serves; ordinary HTTP negotiation
selects one:

| Transport | Selected by | Response shape |
|---|---|---|
| `unary` | default | One buffered HTTP response. |
| `stream` | `Accept: text/event-stream`, or the advertised streaming media type | Streaming response; terminal usage may be an HTTP trailer. |
| `multipart` | request `Content-Type: multipart/form-data` | One buffered HTTP response. |

A broker MUST reject an unsupported selection with
`protocol_transport_unsupported` before authorization admission.

## 3. Request

`POST /v1/job`

Required headers:

| Header | Meaning |
|---|---|
| `Livepeer-Protocol: paid-job/v1` | Protocol identifier. |
| `Livepeer-Capability` | Capability identifier. |
| `Livepeer-Offering` | Offering identifier. |
| `Livepeer-Request-Id` | Caller-selected UUID and idempotency key. |
| `Livepeer-Authorization` | Base64 deterministic-protobuf `SpendAuthorization`. |

`Livepeer-Payment` is optional. If present, it funds an aggregate account
shortfall as part of the atomic admission. It MUST NOT authorize work by
itself. A missing authorization is `401 authorization_required`; the broker
MUST reject it before processing a payment or invoking a runner.

The authorization payload MUST bind:

- domain `livepeer-spend-authorization/v1`;
- payer, payee, chain, and denomination;
- the broker URI, protocol, capability, and offering;
- the accepted quote and exact work-unit price;
- this `Livepeer-Request-Id`;
- SHA-256 of the exact request body;
- maximum cumulative units and maximum debit;
- issuance, expiry, nonce, and a payer-scoped authorization ID; and
- an optional delegated caller public key.

For jobs, `session_id`, predecessor authorization, and revision MUST be
empty or zero. A broker MUST verify every binding before admission. If a
`caller_public_key` is present, the request MUST also carry the
`Livepeer-Caller-Proof` defined by the header contract.

The workload body is passed to the runner according to the selected offering.
Capability-specific fields are outside this protocol.

## 4. Admission and execution

Admission is one durable atomic operation:

1. validate the authorization signature, scope, time window, and request
   digest;
2. if an optional payment exists, validate and credit it to the stable
   account exactly once;
3. reserve the authorization's maximum debit from account availability;
4. record the authorization and request idempotency state; then
5. allow runner invocation.

Insufficient account availability is `402 insufficient_balance`. The
request MUST NOT run. The caller may fund the account separately through
`POST /v1/payment/account/fund`, or retry the same request with exact
shortfall funding. Funding targets aggregate float; it is not sized to the
job's maximum unless that is the payer's deliberate float policy.

The broker MUST cap billing at the signed cumulative maximum. Work delivered
beyond that maximum is seller risk and MUST NOT create payer debt.

## 5. Idempotency

`Livepeer-Request-Id` and `authorization_id` are durable idempotency keys.

- An identical retry MUST replay the recorded admission or terminal
  accounting outcome without another reservation, payment credit, runner
  execution, or debit.
- A retry while the original is executing MUST return `job_in_flight`.
- Reuse of either identifier with altered authorization, payment, route, or
  body MUST fail with `request_id_reuse` or the corresponding authorization
  conflict.
- Terminal records MUST survive restart and be retained for the advertised
  reconciliation window.

A replay returns accounting metadata, not necessarily the runner response
body. Brokers are not required to persist arbitrary generated content.

## 6. Usage and settlement

The broker measures usage with the extractor frozen for the selected
offering. A failed non-streaming runner response that produced no billable
output settles zero units. Partial streaming output is billable according to
that extractor.

Settlement calls `SettleAuthorization` with actual billable units, capped at
the signed maximum. It atomically:

- debits the stable account for the price of those units;
- releases unused reservation;
- marks the authorization terminal; and
- returns the resulting account version.

If settlement is temporarily unavailable after output was delivered, the
exchange is `accounting_pending`. The broker MUST durably retry the same
authorization settlement until it reaches the daemon's recorded result.
Retries MUST NOT execute work again or settle a second time. An operator
timeout may alert or wind down service, but MUST NOT manufacture a
`DEBIT_FAILED` terminal record or write off the payer obligation.

The signed `SettlementRecord` MUST preserve:

- accepted quote, work unit, measured units, and billed units;
- authorized, reserved, funded, debited, and released value as distinct
  quantities;
- authorization ID, caller request ID, broker job ID, and account version;
- issuance time and terminal outcome.

For a job, `work_id` carries the authorization ID for wire compatibility; it
is not a recipient-random ticket-session identity.

## 7. Response and reconciliation

The broker returns:

| Header or trailer | Meaning |
|---|---|
| `Livepeer-Job-Id` | Broker identifier for this exchange. |
| `Livepeer-Work-Unit` | Frozen work-unit name. |
| `Livepeer-Work-Units` | Broker's measured usage claim. |
| `Livepeer-Settlement` | Signed terminal settlement when available on the response. |

Streaming responses MUST announce any HTTP trailer they use. Because many
client stacks cannot read trailers, durable lookup is authoritative:

- `GET /v1/exchange/{request_id}` reports admission and accounting state
  using the caller's own ID.
- `GET /v1/settlement/{job_id}` returns the signed terminal settlement.
- a nonterminal exchange returns `202 accounting_pending`, not a zero claim.

SDK callbacks and response trailers reduce reconciliation latency; they are
not correctness dependencies. A raw HTTP caller can query the same durable
surfaces.

## 8. Errors

The broker fails closed before runner execution for:

- missing, malformed, expired, incorrectly signed, or replay-conflicting
  authorization;
- route, quote, price, payer, payee, chain, denomination, request ID, body
  digest, or caller-proof mismatch;
- insufficient account availability;
- unsupported transport or unavailable offering; and
- a payment-only request.

Malformed optional funding MUST fail the whole admission without losing an
already-recorded idempotent credit.

## 9. Compatibility and cutover

Authorization-backed wholesale accounting is intrinsic to
`paid-job/v1`. It is not enabled by
`extra.features.wholesale_accounts` or another offer flag.

Operators upgrading from ticket-session workload payment MUST drain legacy
in-flight jobs and accounting retries before starting an authorization-only
broker. An authorization-only broker MUST refuse startup or recovery when
durable nonterminal workload records lack authorization state. It MUST never
silently fall back to `OpenSession -> ProcessPayment -> DebitBalance` for a
workload request.

Ticket validation and recipient-random generations remain available only to
fund the wholesale account. Rotation of a funding generation cannot alter a
job authorization, cause runner replay, or strand account credit.

## 10. Conformance

Conformance covers:

- payment-only refusal before ticket processing;
- funding-free admission from existing account value;
- optional exact-shortfall funding;
- route, quote, digest, caller-proof, expiry, and maximum validation;
- concurrent reservation exclusion;
- request and authorization replay;
- measured settlement and unused reservation release;
- durable `accounting_pending` recovery; and
- restart refusal for undrained legacy records.

## Changelog

| Version | Date | Change |
|---|---|---|
| 1.2.0-draft | 2026-09-11 | Makes single-purpose spend authorization and stable wholesale-account settlement the only paid-job path. Tickets are funding instruments only; payment-only workload admission and bounded debit write-off are removed. |
| 1.1.0-draft | 2026-08-21 | Added durable settlement lookup, request binding, and non-admission evidence. |
| 1.0.0-draft | 2026-08-10 | Unified unary, streaming, and multipart paid jobs. |
