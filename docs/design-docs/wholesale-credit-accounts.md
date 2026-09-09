---
title: Fair wholesale credit accounts and delegated invocation
status: accepted
last-reviewed: 2026-09-09
beads: lnm-b41
---

# Fair wholesale credit accounts and delegated invocation

## Decision

Livepeer payment value belongs to a stable **payer-payee wholesale account**,
not to an individual request, customer, capability, or rotating `work_id`.
Tickets fund that account. A distinct, signed, single-purpose authorization
allows exactly one job or session to reserve and debit a bounded amount from
it. Settlement charges actual delivered work and releases the remainder.

This design supports both common caller shapes:

1. An **in-path capability provider** authenticates its customer, authorizes
   payment, invokes the broker, and returns the result.
2. An **out-of-path funding intermediary** authenticates its customer and
   authorizes payment, but hands the selected broker and authorization to the
   customer. The customer invokes the broker directly.

The protocol names neither product shape. Both use the same wholesale account
and authorization contract.

## Why the current request-funded model is economically wrong

A maximum workload allowance answers, "How much may this engagement spend?"
It does not answer, "How much new value should the payer transfer now?"

Collapsing those questions causes a payer to send the worst-case expected
value for every request. When actual usage is small, residual value accumulates
under payee-side `work_id` ledgers. Rotation, route changes, and closure can
then strand real economic value. Repeating this across callers and payees turns
a safety margin into an unbounded treasury loss.

The invariant is instead:

```text
issued ticket EV = settled wholesale charges + bounded reusable account float
```

It must never trend toward the sum of request maxima.

## Quantities that must remain separate

| Quantity | Meaning |
|---|---|
| Customer authorization | The most the customer-facing provider permits an engagement to cost |
| Wholesale reservation | Account credit temporarily unavailable to concurrent engagements |
| Ticket expected value | New economic value added to the payer-payee account |
| Settled debit | Actual wholesale charge for delivered work |
| Available credit | Credited value not reserved or already debited |

`max_tokens`, maximum media duration, maximum characters, an image-step cap,
or any other workload ceiling is customer authorization. It is not a command
to mint that much ticket EV.

## Account identity and ownership

The economic account is keyed by stable network principals:

```text
(chain, payer identity, payee identity, denomination)
```

Capability, offering, quote, broker endpoint, request, session, and `work_id`
are not account owners. They belong to reservations and settlement records.

`work_id` remains useful as a ticket-validation generation. A recipient-random
rotation may create a new generation, but it cannot discard, hide, or silently
reassign the stable account balance.

For a payer serving multiple customers, residual wholesale credit belongs to
the payer. It may fund a later authorized customer engagement with the same
payee. No customer owns or can directly spend pooled wholesale credit.

## Account state

The receiver maintains a durable, auditable account with at least:

- total credited ticket EV;
- total settled debits;
- currently reserved value;
- available value;
- explicit withdrawal, transfer, or drain adjustments when supported; and
- the active ticket-validation generation set.

The conservation rule is:

```text
credited + inbound adjustments
  = available + reserved + settled debits + outbound adjustments
```

All mutations are atomic and idempotent. Two concurrent engagements cannot
reserve the same available value.

## Single-purpose spend authorization

The caller never receives generic account authority. It receives an
authorization for one exact engagement. The language-neutral authorization
must bind:

- payer identity;
- payee and broker identity;
- authorization ID and nonce;
- payer-generated request or session ID;
- capability, offering, and protocol;
- immutable quote or pricing-curve identity;
- work-unit name;
- maximum debit;
- validity window;
- job parameters, request digest, or session descriptor commitment as the
  engagement permits;
- caller proof or a narrowly scoped proof-of-possession identity; and
- payer signature and canonicalization version.

The signing schema is generic, but each signed instance is specific to one
workload engagement. A finite request can commit to its body. A live session
cannot commit to future media bytes, so it binds the session descriptor,
authorized caller, route, and maximum cumulative debit instead.

The payment envelope becomes conceptually:

```text
payment envelope
├── single-purpose spend authorization
└── optional account-funding ticket(s)
```

Funding tickets are present only for the account shortfall. They do not grant
the holder authority to spend the rest of the account.

## Route selection and failover

The caller may express route constraints or select from payer-provided
candidates. The payer must validate and lock the final payee, broker, offering,
and quote before signing the authorization because both account location and
price depend on that route.

An authorization cannot move to a different payee. Failover requires another
authorization. The payer must either:

- retain both customer and wholesale reservations until the first authority is
  irrevocably retired; or
- receive authoritative proof that the first broker never admitted it before
  releasing the first reservations.

A timeout or caller assertion is not such proof.

## Finite job lifecycle

```mermaid
sequenceDiagram
    participant C as Caller
    participant P as Payer service
    participant S as Sender daemon
    participant B as Capability broker
    participant R as Receiver daemon

    C->>P: request job authorization + route constraints + maximum
    P->>P: hold customer funds; lock route
    P->>S: ensure bounded account float / mint shortfall idempotently
    S-->>P: optional funding ticket(s)
    P->>B: submit shortfall to account/fund (when out of path)
    B->>R: credit aggregate account; admit no work
    P-->>C: broker + single-purpose authorization
    C->>B: invoke exact job
    B->>R: atomically admit authorization, apply funding, reserve maximum
    R-->>B: admitted
    B->>B: execute and measure actual units
    B->>R: settle actual debit; release remainder
    B-->>C: result + signed settlement
    P->>B: query by payer-issued request ID if callback is absent
    B-->>P: signed admission / settlement state
    P->>P: settle customer ledger and release hold
```

The SDK may submit the settlement immediately for low latency, but financial
correctness cannot depend on SDK behavior. A caller may use raw HTTP, crash,
lose the response, or intentionally omit the callback. The payer therefore
retrieves broker-authoritative state using the payer-issued request ID and
verifies the signed record against its immutable route snapshot.

An in-path provider may instead attach the optional shortfall ticket to the
same authorized invocation. An out-of-path intermediary submits that ticket
itself to `POST /v1/payment/account/fund`; it does not depend on the end user
to deliver wholesale value. Both flows credit the same account and neither
turns the ticket into workload authority.

Authorization has a durable terminal state:

```text
issued -> admitted -> settled
   |          \----> outcome-unknown/operator resolution
   \-> expired-unused
```

`expired-unused` must be irrevocable at the receiver: after it is reported,
the authorization can never be admitted or debited. Only that property makes
automatic reservation release safe.

## Long-running session lifecycle

A session maximum is permission, not prepayment. The payer holds the customer
maximum according to its customer-billing policy and signs one session-scoped
cumulative debit cap. The receiver reserves and debits a small runway from the
shared account as verified usage advances.

Account funding is threshold-driven across all sessions with that payee:

```text
target available float = expected near-term unreserved demand + safety buffer
funding shortfall      = max(0, target available float - available funded value)
```

The payer mints only that aggregate shortfall. Active reservations are already
absent from available value, so adding them again to the target would
double-count exposure. It does not mint each session's full ceiling and does
not require the media caller to carry generic refill tickets. If payer-side
replenishment becomes unavailable, the broker may work only through already
authorized and funded runway, then winds down without extending involuntary
credit.

For bounded sessions, the initial authorization cannot grow. For extensible
sessions, increasing the cumulative cap requires a new idempotent authorization
revision from the payer. The revision remains bound to the same session and
cannot be replayed as a separate engagement.

## Workload-agnostic application

Funding behavior follows engagement properties, not capability names:

- finite work with known or tolerably bounded cost uses a job reservation;
- progressive or interruptible work uses bounded runway;
- long-lived work uses a session cumulative cap and runway debits; and
- atomic variable-cost work that cannot pause still requires worst-case
  reservation, seller credit, or conditional escrow—but not repeated transfer
  of the worst-case value when reusable account credit already exists.

This covers chat, image generation, transcription, speech generation, VOD/ABR,
live media, and open-world custom capabilities without adding payment enums.

## Customer ledger is separate from wholesale accounting

The payer service may expose any of these customer plans:

- wholesale wei pass-through;
- wholesale cost plus a disclosed fee; or
- independent retail pricing in USD or another unit.

The Livepeer relationship is unchanged: the payer service is the network payer
and the orchestrator is the wholesale payee.

Customer holds, charges, refunds, API-key attribution, discounts, and retail
prices live only in the payer service. The payee sees no customer balance or
retail price. Signed broker settlement is wholesale cost evidence. Customer
billing may use that actual usage under a separate retail price snapshot.

When independent retail pricing is supported, the payer service must keep two
linked but independent ledgers:

1. customer currency and pricing per tenant; and
2. wholesale wei credit per payer-payee account.

They share an opaque correlation ID, not balances or authority. The payer bears
ticket variance, currency conversion, margin, route fragmentation, promotional
credit, and customer credit risk.

## Component responsibilities

### Language-neutral protocol

- Define account, authorization, canonical signature, and state-query schemas.
- Extend `paid-job/v1` and `paid-session/v1` without capability-specific fields.
- Specify irrevocable unused expiry and signed terminal evidence.
- Specify idempotency and failover semantics.

### Payment daemon sender

- Maintain bounded target-float policy per payer-payee account.
- Mint only an idempotent shortfall.
- Enforce operator maximums against actual returned ticket EV.
- Treat ticket generations as replaceable validation state, not balance owners.

### Payment daemon receiver

- Persist stable accounts and the conservation ledger.
- Atomically apply funding, admit authorization, and reserve value.
- Settle actual debit and release unused value.
- Prevent replay, over-reservation, post-expiry admission, and rotation loss.
- Expose authenticated account and authorization status needed for recovery.

### Capability broker

- Verify route, quote, caller, request/session, and workload commitment.
- Start backend work only after atomic admission.
- Drive actual usage debits and signed settlement.
- Expose durable request/session lookup independent of caller callbacks.

### Payer service or gateway

- Authenticate customers and enforce customer holds/caps.
- Lock the route before signing authority.
- Correlate customer and wholesale records without exposing tenant identity.
- Treat SDK reporting as an optimization and reconcile directly with brokers.
- Keep pass-through and retail accounting policy outside the Livepeer protocol.

### Service registry

No new economic responsibility. It continues to provide route, payee, protocol,
price, and settlement-key facts that the payer snapshots into an authorization.

## Safety and exposure policy

Each payer configures, per payee and in aggregate:

- target float and maximum float;
- maximum aggregate EV based on the tickets actually returned;
- maximum winning face value for one probabilistic ticket;
- maximum reservation and cumulative authorization;
- refill threshold and safety buffer;
- stale-authorization and reconciliation deadlines;
- drain-before-deselect behavior; and
- explicit withdrawal or transfer disposition.

In protocol v1 that disposition is drain-by-consumption: stop assigning new
funding, settle admitted work, and preferentially consume the bounded residue
before deselecting the route. Ticket EV is service credit, not refundable
escrow, so a cash refund or cross-payee transfer requires a separately agreed
adjustment and is never inferred by deleting an account.

Existing balance may be a routing tie-breaker only after compatibility, health,
and effective price. It must not trap a payer on a bad or overpriced route.

## Migration constraints

Legacy `work_id` balances cannot be silently declared equivalent to the new
stable account. Migration must inventory each generation, prevent double
credit, preserve redeemable tickets, and either transfer verified residual or
drain it under the old protocol. Mixed peers fail closed unless a specified
compatibility mode can preserve every accounting invariant.

Migration is fenced per payer-payee route. Before a payer's first account
authorization, it stops issuing legacy envelopes for that route and waits for
its legacy in-flight jobs and session debits to reach durable outcomes. The
receiver can then atomically zero a drained generation and move its verified
residual into the stable account. Different payers may opt in at different
times; one payer MUST NOT run legacy and account-backed debits concurrently on
the same ticket generation. This prevents an account migration from moving
credit that a legacy in-flight debit still expects to consume.

## Non-goals

- Defining customer retail prices or currencies in the network protocol.
- Giving orchestrators customer identities.
- Making SDK use mandatory.
- Letting an authorization select an arbitrary capability or payee after issue.
- Eliminating probabilistic-ticket variance; the design bounds reusable float
  and attributes variance to the payer that chose to fund it.

## Work tracking

Implementation was tracked by [`lnm-b41`](../../.beads/) and completed plan
[`0049-fair-wholesale-credit-accounts.md`](../exec-plans/completed/0049-fair-wholesale-credit-accounts.md).
