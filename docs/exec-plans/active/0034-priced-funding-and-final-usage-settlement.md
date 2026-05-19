---
plan: 0034
title: Priced funding and final-usage settlement across gateway, broker, and payment-daemon
status: active
phase: design
opened: 2026-05-19
owner: harness
related:
  - "active plan 0024 — quote-free ticket-params flow across gateway, broker, and payment-daemon"
  - "completed plan 0005 — payment-daemon component"
  - "completed plan 0014 — wire-compat envelope + sender daemon"
  - "completed plan 0015 — interim debit cadence design"
  - "completed plan 0016 — chain-integrated payment-daemon — design choices"
  - "completed plan 0021 — vtuber top-up control ws"
---

# Plan 0034 — priced funding and final-usage settlement across gateway, broker, and payment-daemon

## 1. Problem

The current rewrite payment path collapses three different concepts into one input:

- accepted unit price
- upfront funded budget
- final billed work

That simplification is no longer defensible for real workloads.

Examples:

- `openai:chat-completions` knows prompt tokens before execution, but only knows total
  prompt + completion tokens after execution.
- rerank often knows a strong upfront estimate, but may still differ if truncation,
  request rejection, or partial execution occurs.
- streaming / session workloads naturally consume work over time and require budget
  top-ups or stop-at-budget behavior rather than a single exact upfront bill.

The current sender-mode daemon behavior proves the mismatch:

- `CreatePayment(...)` receives only recipient, capability, offering, broker URL, and
  total `face_value`.
- sender-mode `payment-daemon` serializes `Payment.expected_price` as a canonical zero
  value on the quote-free path.
- gateways that correctly expect unit-price provenance cannot reconcile or account for
  the work because the payment blob lacks the quote that justified the budget.

The system therefore lacks a trustworthy contract for:

- what price the gateway accepted
- what budget the gateway funded
- what work the broker/backend actually measured
- how the final owed amount was derived

## 2. Required invariant

Every paid execution must preserve and expose four facts independently:

1. **Accepted price basis**
   - the payer accepted a specific unit-pricing contract
2. **Funded budget**
   - the payer funded or authorized some amount against that contract
3. **Measured usage**
   - the broker/backend measured actual work in workload-native terms and canonical
     billed units
4. **Settlement outcome**
   - the final billed value came from `accepted price × billed units`, not from an
     opaque precomputed total

These facts must be visible to:

- the gateway
- sender-mode `payment-daemon`
- capability broker
- receiver-mode `payment-daemon`
- operator/debug surfaces used for reconciliation

In addition, the source of truth for those facts must be explicit:

1. **Quote authority**
   - the accepted price basis must come from a route/quote artifact with stable identity,
     not from ad-hoc caller-supplied numeric fields alone
2. **Settlement authority**
   - the broker-side accounting path is authoritative for `actual_units`, `billed_units`,
     and `billed_value_wei`
3. **Gateway recordkeeping**
   - the gateway persists the accepted quote identity, funded budget, and returned
     settlement record against each request/session

## 3. Model

### 3.1 Separate quote from budget from final usage

The rewrite should represent payment as three distinct layers:

| Layer | Meaning | Example |
|---|---|---|
| accepted price | unit pricing contract the gateway agreed to | `25_000_000 wei per 1 token` |
| funding | budget funded up front or during the session | `232_600_000_000 wei` |
| usage | estimated before execution, actual after execution | `9304 estimated tokens`, `9471 actual tokens` |

`face_value` remains necessary, but only as the funded value of a ticket batch. It is
not sufficient to describe the economic contract by itself.

### 3.2 Quote identity, not just quote numbers

The accepted price basis must be anchored to a stable quote/route identity. Numeric
price fields alone are not enough because they do not capture:

- which route/offering revision was selected
- which constraint set or backend-scoring version applied
- whether the broker is validating against the same advertised price basis the gateway
  accepted

The gateway therefore needs a quote artifact with a stable identity, such as:

- `quote_id`
- `quote_version`
- `constraint_fingerprint`
- `route_fingerprint`

The exact shape is up for design, but the contract requirement is not: sender mode and
broker settlement must be able to refer to the same accepted quote identity.

### 3.3 Canonical work accounting

Every paid workload must expose a canonical billed unit:

- chat: tokens
- rerank: ranked documents or requests, depending on offer definition
- image generation: images, pixels, steps, or another explicitly declared unit
- media / streaming: seconds, frames, pixels, or another explicitly declared unit

The broker already publishes `work_unit.name` in offers. This plan makes that unit part
of the payment and settlement contract, not just a registry detail.

### 3.4 Estimated vs actual usage

The protocol must support both:

- `estimated_units`
  - known before execution and used to choose initial funding
- `actual_units`
  - known only after some or all work completes

For variable-cost workloads, final settlement uses `actual_units`.

For exact-cost workloads, `estimated_units == actual_units` remains valid and cheap.

### 3.5 Numeric types are part of the contract

Arithmetic fields must use numeric wire types, not free-form strings.

Guidance:

- counts such as `estimated_units`, `actual_units`, and `billed_units` should use
  `uint64` unless a stronger reason emerges
- money-like fields should use a fixed canonical representation fit for arithmetic and
  round-tripping
- if `uint64` is insufficient for a money field, use an explicit byte encoding or a
  dedicated big-integer message, not decimal strings

Human-readable strings may still appear in logs and operator APIs, but not as the
primary proto representation for arithmetic inputs.

## 4. Proposed contract changes

### 4.1 Extend `CreatePaymentRequest`

`CreatePaymentRequest` must stop being a face-value-only RPC. It should carry the
accepted pricing basis and the intended funding scope.

Add fields conceptually equivalent to:

- `accepted_quote_ref`
  - `quote_id`
  - `quote_version`
  - `constraint_fingerprint`
  - `route_fingerprint`
- `accepted_price`
  - `price_per_unit_wei`
  - `units_per_price`
  - `work_unit_name`
  - `capability`
  - `offering`
  - quote-bound identifiers needed for broker validation
- `estimated_units`
- `funded_value_wei`
- optional `max_total_units`
- optional `top_up_allowed`

The sender daemon then becomes responsible for validating that:

- the quote reference is well-formed
- `funded_value_wei` is coherent with the accepted quote and intended initial budget
- recipient/capability/offering match the ticket-params context it fetched

The quote reference is not optional architecture sugar. It prevents drift between:

- the route selection artifact the gateway accepted
- the payment the sender daemon minted
- the settlement basis the broker validates

### 4.2 Populate `Payment.expected_price`

`Payment.expected_price` must no longer be emitted as a zero-valued placeholder on the
quote-free path.

Instead:

- sender-mode `payment-daemon` copies the accepted quote basis into the wire payment
- the payment blob therefore carries both:
  - funded ticket economics
  - payer-accepted unit price basis

This restores the intended meaning of `expected_price`:

- a payee-visible statement of the payer's last-known accepted unit price for the work

`PriceInfo` alone is not enough for the full contract. It must be paired with the quote
identity described above so the broker can validate that the payer is referring to the
same priced route/version it offered.

### 4.3 Add settlement / usage reporting

The broker path must surface actual metered usage and final billing outcome, not just
the backend response payload.

Add a settlement/result object conceptually containing:

- `work_unit_name`
- `estimated_units`
- `actual_units`
- `billed_units`
- `billed_value_wei`
- `funded_value_wei`
- `accepted_quote_ref`
- `settlement_outcome`
  - exact
  - underfunded
  - overfunded
  - stopped_at_budget
  - topped_up
- workload-specific breakdown

Workload-specific breakdowns should remain extensible metadata, not core arithmetic
fields. The canonical settlement record should stay small and stable:

- accepted unit basis
- funded value
- actual billed units
- billed value
- optional breakdown metadata

Examples of optional breakdown metadata:

- chat:
  - `input_tokens`
  - `output_tokens`
- image/video:
  - workload-defined measured fields

### 4.4 Budget top-up and stop semantics

For long-running or variable workloads, the system must support one of these explicit
policies:

- fund enough upfront and reconcile at completion
- top up while work is in flight
- stop the job when remaining funded budget is exhausted

This choice cannot remain implicit inside adapters. The protocol and broker accounting
surfaces must expose it.

For streaming and long-lived workloads, this requires an explicit session model rather
than bolting long-lived semantics onto a unary mint call. The rewrite should treat:

- request-scoped unary funding
- session-scoped funding/top-up
- final session settlement

as related but distinct protocol flows.

## 5. Workload classes

### 5.1 Unary exact workloads

Examples:

- rerank with exact request-count billing
- fixed-count image jobs

Behavior:

- gateway computes `estimated_units`
- execution usually yields `actual_units == estimated_units`
- settlement reports the exact same value

No top-up is needed, but the price/funding/usage split still applies.

### 5.2 Unary variable workloads

Examples:

- `openai:chat-completions`
- text generation where output length is not known upfront

Behavior:

- gateway knows some usage before execution and estimates the rest
- initial funding is based on estimate and policy
- final usage is measured after completion
- final billed amount is derived from the accepted unit price and measured units

### 5.3 Streaming / long-lived workloads

Examples:

- realtime websocket sessions
- RTMP/media pipelines
- long-running generation streams

Behavior:

- gateway funds initial runway
- broker/receiver debit against balance over time
- gateway may top up
- work stops or degrades when budget is exhausted unless policy allows otherwise

This plan does not replace the existing session-meter patterns; it makes their pricing
and accounting inputs explicit and interoperable with unary flows.

## 6. Component responsibilities

### 6.1 Gateway

The gateway must own and persist:

- accepted quote identity
- accepted quote metadata
- estimated units
- funded amount
- actual usage returned by settlement
- final billed amount

That data is needed for:

- retail billing
- user-visible reconciliation
- retry logic
- budget enforcement
- analytics and dispute/debug tooling

### 6.2 Sender-mode `payment-daemon`

The sender daemon must:

- accept explicit quote metadata from the gateway
- fetch canonical payee ticket params
- mint tickets against the funded amount
- serialize the accepted price basis into `Payment.expected_price`
- reject incoherent requests rather than fabricating zero-valued pricing context

### 6.3 Capability broker

The broker must:

- validate that the incoming payment references a meaningful accepted quote/route state
- meter actual work
- act as the authoritative source for settlement data
- emit settlement metadata alongside backend execution outcome
- coordinate top-ups or stop-at-budget behavior for long-lived workloads

### 6.4 Receiver-mode `payment-daemon`

The receiver daemon remains the authority for:

- truthful ticket params
- ticket validation
- balance credit/debit
- on-chain redemption

This plan does not move pricing ownership into the daemon. It only ensures the daemon
sees the same accepted-price context that the gateway used when funding the request.

## 7. Proto and API changes

### 7.1 `livepeer.payments.v1`

Add or evolve messages so sender RPC accepts quote + funding metadata and the wire
`Payment` carries nonzero `expected_price`.

Likely touchpoints:

- `payer_daemon.proto`
- `types.proto`

This plan replaces the current face-value-only daemon-app contract. It does not preserve
that contract for long-term use.

### 7.2 Concrete proto sketch

The following sketch is the intended direction for cross-team review. It is not yet the
checked-in contract.

```proto
message QuoteRef {
  // Stable identity of the route/quote artifact the gateway accepted.
  string quote_id = 1;

  // Monotonic version or generation for that quote artifact.
  uint64 quote_version = 2;

  // Stable digest of the priced route constraints used during selection.
  bytes constraint_fingerprint = 3;

  // Stable digest of the selected route/backend identity as seen by the gateway.
  bytes route_fingerprint = 4;
}

message AcceptedPrice {
  // Wei charged per `units_per_price` billed units.
  uint64 price_per_unit_wei = 1;

  // Denominator for the unit price. Reuses the old PriceInfo semantics without
  // carrying the legacy field name into the daemon-app RPC.
  uint64 units_per_price = 2;

  // Canonical billed unit name, e.g. "token", "request", "second".
  string work_unit_name = 3;

  // Canonical capability string the gateway selected.
  string capability = 4;

  // Canonical offering string the gateway selected.
  string offering = 5;

  // Stable reference back to the accepted quote / route artifact.
  QuoteRef quote_ref = 6;
}

message FundingIntent {
  // Gateway estimate before execution begins.
  uint64 estimated_units = 1;

  // Amount funded in this ticket batch. If a big-int representation becomes necessary,
  // replace with a dedicated bytes/message type before shipping.
  uint64 funded_value_wei = 2;

  // Optional maximum units the gateway authorizes before a top-up or stop is required.
  uint64 max_total_units = 3;

  // Whether the gateway allows follow-on funding for this request/session.
  bool top_up_allowed = 4;
}

message CreatePaymentRequest {
  // Recipient orchestrator/payee identity.
  bytes recipient = 1;

  // Selected broker origin for ticket-params retrieval and settlement validation.
  string ticket_params_base_url = 2;

  // Accepted quote/price basis the gateway selected.
  AcceptedPrice accepted_price = 3;

  // Funding scope for the initial payment batch.
  FundingIntent funding = 4;
}

message CreatePaymentResponse {
  // Wire-format livepeer.payments.v1.Payment bytes.
  bytes payment_bytes = 1;

  // Tickets minted in this batch.
  uint32 tickets_created = 2;

  // Aggregate expected value committed by this batch.
  uint64 expected_value_wei = 3;

  // Echo of the effective funded value used for the minted payment batch.
  uint64 funded_value_wei = 4;

  // Echo of the quote identity sender mode serialized into the payment context.
  QuoteRef accepted_quote_ref = 5;
}

message SettlementRecord {
  // Accepted quote identity this settlement resolved against.
  QuoteRef accepted_quote_ref = 1;

  // Canonical billed unit name.
  string work_unit_name = 2;

  // Gateway estimate captured at funding time.
  uint64 estimated_units = 3;

  // Actual measured units from broker-side accounting.
  uint64 actual_units = 4;

  // Units the broker actually billed.
  uint64 billed_units = 5;

  // Amount funded/authorized for this request or settlement window.
  uint64 funded_value_wei = 6;

  // Final amount billed by the broker.
  uint64 billed_value_wei = 7;

  enum SettlementOutcome {
    SETTLEMENT_OUTCOME_UNSPECIFIED = 0;
    EXACT = 1;
    UNDERFUNDED = 2;
    OVERFUNDED = 3;
    STOPPED_AT_BUDGET = 4;
    TOPPED_UP = 5;
  }
  SettlementOutcome outcome = 8;

  // Optional workload-specific metadata. Not part of canonical arithmetic.
  map<string, string> breakdown = 9;
}
```

Notes:

- `uint64` is the preferred starting point for counts and money-like arithmetic fields.
  If a field is proven too small, migrate that field deliberately; do not start with
  decimal strings.
- The on-the-wire `Payment.expected_price` can continue to use the existing wire-compat
  `PriceInfo`, but sender mode must populate it from `AcceptedPrice` instead of
  serializing a zero value.
- The broker validates the quote identity and emits the authoritative `SettlementRecord`.
- The gateway stores both the accepted quote artifact and the returned settlement record
  for request/session reconciliation.

### 7.3 Broker HTTP surfaces

Add a settlement-capable paid-path response contract for request/response workloads.

For unary HTTP modes, the broker should return settlement metadata with or alongside the
backend result so gateways can persist final accounting.

For streaming/session modes, settlement metadata must be available via:

- stream trailers / terminal events, or
- a broker session-status endpoint with stable request/session correlation

## 8. Execution

### Phase 1 — immediate correctness fix

- fix current sender-mode `expected_price` population so newly minted payments stop
  serializing zero-valued price info when the necessary quote data is available
- ship seed-correctness fixes and any other blocking wire bugs independently of the
  larger contract redesign

### Phase 2 — contract design

- define canonical quote/funding/usage vocabulary in the protocol docs
- define how `PriceInfo` maps to gateway-facing quote structures
- define stable quote identity and validation rules
- decide how constraint/version fingerprints are represented
- choose canonical numeric wire types for counts and money-like fields

### Phase 3 — proto and daemon changes

- extend `CreatePaymentRequest`
- populate `Payment.expected_price` from accepted quote data
- reject missing/invalid quote metadata in sender mode
- regenerate committed protobuf bindings

### Phase 4 — broker accounting and settlement

- add actual-usage settlement surfaces on paid paths
- plumb canonical billed units through mode drivers / extractors
- make stop/top-up policy explicit for long-lived flows
- ensure broker-side settlement is the authoritative record returned to gateways

### Phase 5 — gateway adoption

- persist accepted quote metadata per request/session
- pass quote + funding metadata into `CreatePayment`
- consume and store settlement metadata
- update retail billing and retry logic around underfund / overfund / topped-up flows

### Phase 6 — cutover

- switch all callers to the quote-aware `CreatePayment`
- require broker settlement responses on paid execution paths
- make gateway reconciliation depend on settlement records rather than local inference

## 9. Non-goals

This plan does not:

- redesign on-chain winning-ticket economics
- force every workload to use tokens as its work unit
- require every unary workload to implement top-up behavior
- replace existing backend-specific usage metering logic; it standardizes the contract
  that carries the result

## 10. Success criteria

The rewrite is done when all of the following are true:

1. Gateways can explain, for any request:
   - which quote/route identity they accepted
   - what unit price they accepted
   - how much budget they funded
   - how much work actually ran
   - how final billed value was derived
2. Sender-mode `payment-daemon` no longer emits zero-valued `expected_price` for
   quote-free funded requests.
3. Brokers return enough settlement metadata for gateways to perform correct billing and
   reconciliation on both unary and streaming workloads.
4. Variable-cost workloads such as chat no longer rely on "exact upfront face value"
   assumptions to remain economically correct.
