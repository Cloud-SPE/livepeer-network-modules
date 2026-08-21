---
spec_name: paid-job
version: 1.0.12-draft
status: draft
last_updated: 2026-08-18
---

# Protocol: `paid-job/v1`

One paid exchange: a gateway sends one request with a payment envelope, the
broker forwards it to a backend, one response comes back, and the work is
settled once. There is no session object, no lease, and no control plane —
if the workload needs any of those, it is `paid-session/v1` work.

`paid-job/v1` replaces the request-family interaction modes
(`http-reqresp@v0`, `http-stream@v0`, `http-multipart@v0`). What used to be
three mode names is one protocol with a **transport** dimension negotiated
per-request by ordinary HTTP mechanics, so one offering can serve unary and
streaming callers without duplicating itself.

The key words MUST, MUST NOT, SHOULD, and MAY are to be interpreted as in
RFC 2119.

## 1. Roles and trust context

The broker is the **seller's admission edge**: it validates payment before any
backend work happens and emits the seller's usage claim afterward. The gateway
is the **buyer's edge**: the response transits it, so the gateway meters there
(response body, byte counts, its own clock) and bills its customers from its
own signal. `Livepeer-Work-Units` is therefore a *claim*, not a billing
instruction — it feeds the buyer's runway accounting and the divergence
tripwire defined in the trust-model doc. Neither side trusts the other's
meter, and the protocol does not pretend otherwise.

## 2. Transports

An offering declares which transports it serves; a request selects one by
standard HTTP negotiation. The broker MUST NOT require distinct offerings per
transport.

| Transport | Selected by | Response shape |
|---|---|---|
| `unary` | default | One buffered HTTP response. |
| `stream` | `Accept: text/event-stream` (or the offering's declared streaming media type) | Chunked/SSE body; usage claim arrives in a trailer. |
| `multipart` | request `Content-Type: multipart/form-data` | One buffered HTTP response. |

A request selecting a transport the offering does not declare is refused with
`protocol_transport_unsupported` before payment validation side effects.

## 3. Wire shape

### 3.1 Request

`POST /v1/job`

Required headers:

| Header | Meaning |
|---|---|
| `Livepeer-Protocol: paid-job/v1` | Protocol and version. Replaces both `Livepeer-Mode` and `Livepeer-Spec-Version`. |
| `Livepeer-Capability` | Capability id. |
| `Livepeer-Offering` | Offering id. |
| `Livepeer-Request-Id` | UUID. The idempotency key (§4). |
| `Livepeer-Payment` | Base64 payment envelope. |

The body is the workload payload, passed to the backend verbatim. The broker
MUST NOT interpret or rewrite it; capability-specific concerns (model ids,
parameters) live inside it and belong to the gateway↔backend contract.

### 3.2 Response

The backend's response passes through the broker verbatim — status, headers
the backend set, and body. The broker adds:

| Header/trailer | Req | Meaning |
|---|---|---|
| `Livepeer-Work-Units` | yes | The seller's usage claim: integer units measured by the offering's declared extractor. Header on `unary`/`multipart`; **HTTP trailer** on `stream` (it cannot be known before the body ends). Every terminal response carries it, including errors (`0` when no billable work occurred). |
| `Livepeer-Work-Unit` | yes | Echo of the offering's declared unit name (e.g. `tokens`, `output_seconds`). Lets the gateway reject unit drift without a registry round-trip. |
| `Livepeer-Job-Id` | yes | Broker-assigned id for this exchange; the audit key joining claim, debit, and idempotency record. |

There is no balance or runway signaling in `paid-job/v1`: one envelope funds
one exchange, and the buyer's overall funding posture is the clearinghouse's
business. Runway is a `paid-session/v1` concept.

**The trailer is not the only channel, and MUST NOT be.** Go reads HTTP
trailers; HTTPX, the Fetch API and reqwest do not, so a claim delivered
only that way is unreachable for most SDK stacks — leaving a caller to
choose between billing zero, which fails open financially, and blocking.

A broker MUST therefore also serve the terminal claim from
`GET /v1/settlement/{id}`, keyed by the `Livepeer-Job-Id` it returned,
for at least the idempotency window (§4.2). The response carries the
terminal `Livepeer-Work-Units`, `Livepeer-Work-Unit`, and the signed
settlement envelope (`headers/livepeer-headers.md`). An exchange still
running answers `202` with its state rather than a claim: a caller MUST
be able to distinguish "not finished" from "finished at zero", and an
unknown id MUST NOT read as a zero claim.

The trailer stays as the low-latency path for clients that can read it.
It is an optimization, not the contract.

For `stream`, the broker MUST advertise the trailer (`Trailer:
Livepeer-Work-Units`) in the response headers so gateways know to read it,
and MAY advertise `Livepeer-Settlement` the same way.

Conversely, **a broker MUST NOT advertise a trailer on a response that
cannot carry one.** Trailers ride only on chunked responses, so a
`Content-Length` delimited `unary` or `multipart` exchange that names a
trailer is telling a client to wait for something the transport will drop
without a word. On those transports the settlement is retrieved from
`GET /v1/settlement/{id}` — which is the contract on every transport
anyway.
A stream that terminates without the trailer (connection loss) is an
*unclaimed* exchange: the broker still debits what the extractor measured,
the gateway still has its own edge meter, and the divergence policy absorbs
the gap — no recovery handshake exists or is needed at this layer.

## 4. Idempotent open

`Livepeer-Request-Id` is an idempotency key with a broker-enforced contract:

1. The broker MUST record the request id **before** forwarding to a backend,
   in the same durable step as its debit progress for the job.
2. A retry bearing a request id whose exchange reached a terminal outcome
   MUST return that outcome's status, `Livepeer-Work-Units`, and
   `Livepeer-Job-Id` without re-executing the backend and without a second
   debit. Terminal-outcome records MUST be retained for at least the
   operator-configured idempotency window (default 24h).
3. A retry while the original is still in flight MUST be refused with
   `job_in_flight` (retryable), never run concurrently.
4. A replayed request id with a different capability, offering, payment
   envelope, or body hash is a protocol error (`request_id_reuse`), not a
   retry. A retry is byte-identical or it is not a retry.

   Binding the body costs no buffering, which is the objection that
   usually leads implementations to bind its *length* instead — a hole,
   since a changed body of equal length then receives the recorded
   outcome. The envelope is fingerprinted before the exchange (it is all
   that is knowable then) and the body is digested **as it streams** to
   the backend, recorded with the terminal outcome. A replay is drained
   for its digest and compared, which costs a read and no execution.

This is the invariant that deletes the surveyed gateways' hand-rolled
`settle(0)` compensation: a gateway that times out simply retries the same
request id and converges on the true outcome.

### 4.1 What a replay returns, and what it does not

A replay returns the **accounting** outcome, not the backend's response
body. Concretely it MUST return the recorded status, `Livepeer-Work-Units`,
`Livepeer-Work-Unit` and `Livepeer-Job-Id`, and the settlement MUST remain
retrievable at `GET /v1/settlement/{id}`; it MUST NOT re-execute the
backend, debit a second time, or fabricate a body it does not have.

The body is deliberately not replayed. Reproducing it would require every
broker to durably store every customer response — inference output,
transcripts, generated media — for the whole idempotency window, turning a
metering component into a customer-data store with the retention,
encryption and deletion obligations that follow. That is a large liability
to impose on all operators to cover a response lost in flight.

The consequence is explicit rather than hidden: **a caller whose original
response was lost cannot recover the result through this protocol.** It has
paid for work it cannot see. A broker MUST make that distinguishable — a
replayed exchange is marked as such in its body — so the caller can treat
it as a failed delivery of a *charged* exchange rather than as a fresh
result. Deciding what to do then is the caller's: surface the charge,
absorb it, or re-submit under a new request id and pay again.

Operators for whom that trade is wrong MAY offer response retention as an
offering-level feature. It is deliberately not the default and not
required, because the protocol should not oblige every seller to store
every buyer's output.

### 4.5 Funding is checked before the backend runs

A broker MUST NOT execute a backend for a session whose credited balance
cannot cover at least one work unit, and MUST refuse with
`insufficient_balance` (402) instead.

The obvious reading — "usage is unknown until the work runs, so the check
must come after" — is what leaves the hole: for a single exchange there
is no later check at all, since the interim-debit ticker that guards
long-running work never fires. A payee ledger may permit overdraft, and
that is the right primitive for absorbing a probabilistic credit that
lags a debit; deciding whether to *deliver* is the broker's call, and it
should decline what it has not been paid for.

One unit is the floor deliberately, rather than the request's estimate:
payment credit is probabilistic, so a session funded exactly to its
estimate would flap between served and refused. "Can this session afford
anything at all" is the question with a stable answer.

### 4.6 Payment on a rotated-away identity

A payee may retire the recipient rand behind a `work_id` (restart, operator
reset, exhausted nonce space). A payment arriving on a retired identity MUST
be refused, crediting nothing, and surfaced to the caller as
`recipient_rotated` — the same code and the same remedy as
[paid-session](./paid-session.md) §3.3.1, which states the payee's
obligation in full.

The job path has no session to rebind, so the remedy is simply: re-fetch
ticket params, re-mint, retry the job. The distinct code still matters,
because retrying with the same envelope would fail identically forever.

## 5. Usage and settlement

The offering manifest declares the work unit; the extractor that counts it
(`openai-usage`, `bytes-counted`, `response-jsonpath`, `seconds-elapsed`,
`request-formula`, …) is broker host configuration — a seller-side
implementation choice no counterparty gates on, so it is deliberately not
advertised (see `offering-axes.md`). The broker runs the extractor at the terminal
accounting point (response completion or stream termination), debits the
payee-side payment session for exactly the claimed units, and emits the
claim. Claim emission and debit MUST be consistent: the units in
`Livepeer-Work-Units` are the units debited, always.

Failure semantics: an exchange that fails before the backend produced
billable output claims and debits `0`. Partial streaming output is billable
as measured — "how partial is billable" is precisely what the extractor
declaration decides, per offering, not per incident.

**A non-2xx backend response on a non-streaming transport claims `0`
regardless of the extractor.** Leaving that to the extractor makes the rule
an accident of configuration: a usage-reading extractor finds no usage in
an error body and reaches zero on its own, but a request-derived one — a
formula over an image count, a per-request constant — returns its full
count for a request the backend never served. The unit that decides
whether work was billable cannot be the one that never looked at the
outcome.

**Funded ceiling (streams).** A `stream` exchange runs against its envelope's
funded ceiling: the broker MAY terminate the stream once extractor-measured
usage reaches it, ending the body cleanly and claiming exactly the delivered
units in the trailer. This is the seller's fail-closed protection for
long-running exchanges — there is no mid-job refill, deliberately. A workload
that legitimately needs mid-exchange funding is `paid-session/v1` work.

### 5.2 A failed debit is not a settled exchange

A debit that does not land leaves the exchange **delivered but
unsettled**. It is not terminal, and MUST NOT be reported as terminal:

1. **While retry is active**, `GET /v1/settlement/{id}` MUST answer `202`
   with state `accounting_pending`. This is distinct from a job still
   running — nothing further is expected from the backend, only from the
   ledger — so a consumer holds the encumbrance rather than booking it or
   writing it off.
2. **When the debit lands**, the exchange settles normally: a signed
   terminal settlement whose `debited_units` and `billed_value_wei` are
   what the ledger took.
3. **Only when retry is exhausted** does the exchange settle terminal
   with outcome `DEBIT_FAILED`.

Retry MUST be bounded. An unbounded retry produces a job that never
reaches a terminal state, which is worse for a clearinghouse than a clear
loss: an encumbrance it can neither release nor write off. A bound
converts an outage into a recoverable outcome somebody can act on.

Retry MUST reuse the original `debit_seq`. Debits are idempotent by
`(sender, work_id, debit_seq)`, so reusing it cannot double-charge — and
it is what makes the case that motivates retry most, an attempt that
landed and lost its response, safe to repeat.

A broker MUST NOT close the payee session while a debit against it is
outstanding. A closed session refuses debits, so closing guarantees the
retry can never land however generous the budget.

#### What the terminal settlement says

The broker delivers work and debits after, so the ledger call can fail
with the response already gone. When retry is exhausted and the exchange
settles `DEBIT_FAILED`:

- `debited_units` MUST report what the ledger actually took — usually zero,
  or the interim ticks that did succeed on a long exchange. It MUST NOT
  report the measurement.
- `billed_value_wei` MUST report the value that actually moved.
- `actual_units` still reports what the extractor measured. The
  measurement is not in doubt; the payment is.
- The record MUST carry outcome `DEBIT_FAILED`, and a consumer MUST NOT
  treat it as settled.

Reporting the measured units here — which the reference broker did —
makes a broker whose ledger call failed byte-indistinguishable from one
that was paid. A clearinghouse then books revenue that never moved, and
the failure is invisible exactly when it matters.

This is fail-closed on the *accounting*, which is the part still
recoverable once work has shipped. It is deliberately not a claim that the
work was free: the operator has delivered something and has an unpaid
exchange, which is an operational matter — a retry, a dunning process, an
alert — and not something the record should paper over.

Note the ordering limit this does not fix: on a `unary` exchange the
broker commits `Livepeer-Work-Units` in the response headers before the
debit runs, so that header can name units a subsequent debit fails to
take. The settlement is authoritative where the two disagree, and closing
the gap means debiting before header commit.

### 5.1 What binds a job settlement to the job

A paid-job settlement MUST carry both `job_id` and the exchange's
`request_id`, inside the signature.

Neither alone is sufficient. `work_id` is the ticket session's rand hash,
shared by every job minted against it, so it identifies the payment
identity and not the exchange. `job_id` narrows it to one exchange, but the
broker mints it and it reaches a consumer only through the
customer-controlled SDK — the channel the signature exists to distrust. A
clearinghouse holding its own durable job record has no way to bind the
signed evidence to that record without the id it chose itself.

`request_id` is that id: the gateway's `Livepeer-Request-Id` for the
exchange. It is the job path's counterpart to `gateway_session_id` on
paid-session. A broker generates one when the caller sends none, and echoes
it either way, so the field is always populated — it binds to the caller's
own record only when the caller chose it, which is the caller's decision to
make.

### 5.3 Encumbrance on an envelope that may never have been used

A consumer that reserves funds against a payment envelope needs to know
when it can stop holding them. Two questions look alike and are not:

1. **Can this envelope still be spent?** Answerable, but **conditionally**
   — and the condition is easy to miss.

   The TicketBroker checks, at redemption:

   ```solidity
   require(creationRound.add(ticketValidityPeriod) > currRound, "ticket is expired");
   ```

   reading `ticketValidityPeriod` from **current storage**, and the round
   block hash it also requires lives in a permanent mapping that is never
   cleared. So the deadline is not a property of the envelope. **Raising
   `ticketValidityPeriod` extends tickets already issued, and can revive
   ones that had lapsed.**

   A payer therefore reports three things: `creation_round`,
   `expires_after_round` (the last round the envelope can be redeemed in,
   `creation_round + ticket_validity_period - 1`), and the
   `ticket_validity_period` those were computed from. `GetDepositInfo`
   reports `current_round` from the same clock, plus the chain's
   **current** period.

   ```
   not currently spendable  iff  current_round > expires_after_round
                            and  the chain's period is unchanged
   ```

   A consumer MAY compare the period a mint recorded against the chain's
   current one to **detect** that a deadline moved. It is deliberately
   not an instruction to re-encumber: once credit has been finalized or
   returned it may already have been spent or withdrawn, so a later
   increase cannot be undone by re-encumbering, and a protocol that told
   consumers to do so would be describing a recovery that does not exist.
   Treat the comparison as telemetry.

   A missing, zero, or regressing `current_round` stays encumbered — a
   stale clock can only make an expired envelope look live, never the
   reverse.

   Both `ticket_validity_period` fields carry an
   `..._observed_at` timestamp, because a payer may serve the value from
   a short cache rather than reading the contract inside a signing path.
   A value that is "the chain's, as of a moment" is only meaningful with
   the moment attached.

   **There is no unconditional expiry today.** Making the deadline
   immutable needs one of: the validity period snapshotted into the ticket
   and enforced from there, a governance invariant against retroactive
   extension, or some other mechanism that permanently retires the
   envelope. Until one exists, a consumer that finalizes on
   `expires_after_round` accepts the residual risk that governance moves
   it afterwards.

2. **Was this envelope ever used?** **Expiry does not answer this**, and
   the difference is the whole of §5.3.

A customer can submit the envelope, receive work, withhold the job id and
signed settlement from its clearinghouse, wait for expiry, and ask for
the encumbrance back. The exchange happened; the clearinghouse has no
record of it; and the chain shows nothing, because a losing ticket is
never redeemed on-chain and most tickets lose. Chain state cannot
distinguish "never used" from "used and hidden".

#### Terminal outcomes

Because the deadline is conditional, an encumbrance has **four** terminal
outcomes and not two. This replaces an earlier formulation that told
consumers to finalize on expiry and re-encumber if governance later moved
it; that instruction was unsafe and is withdrawn. Finalized credit may
already have been spent or withdrawn, so there is nothing to re-encumber.

| Evidence | Outcome |
|---|---|
| A valid signed settlement | **Settle** on the broker's evidence. |
| No terminal evidence | **Remain unresolved.** Not finalized, not refunded. |
| An operational deadline reached with no settlement | A **distinct conservative full charge** — chosen by the consumer's own policy, not by this protocol, and marked as such rather than presented as a settled exchange. |
| A signed non-admission record | **Retain as attributable audit evidence.** It is NOT refund authority. |

**Automatic refund is not available.** Not on expiry, not on a caller's
assertion, not on a broker's refusal — a broker that received the
envelope can retain and submit it later, so its refusal describes its own
intentions — and not on a non-admission record.

The last of those is the one worth spelling out, because it is the case
that looks closed and is not. `Livepeer-Request-Id` is **not
cryptographically bound into `payment_bytes`**: the signed envelope
carries ticket params, sender, expiration params, ticket sender params
and an expected price whose constraint is
`cap;off;wu;est;qid;qv;cfp;rfp`. No request id, anywhere. So a
non-admission tombstone keyed on a request id does not retire the
envelope — the same envelope can be presented under a different request
id, and a governance increase can revive it after the tombstone was
issued. Non-admission evidence is therefore attributable and useful for
audit and dispute, and insufficient to authorize returning money.

Refund authority requires the protocol to provide one of:

- **immutable ticket validity** — the period snapshotted into the ticket
  and enforced from there, so no later governance change can move a
  deadline already relied upon; or
- **permanent retirement of the exact envelope** — a mechanism that makes
  one specific payment envelope unredeemable regardless of parameters.

Until one exists, a consumer that returns customer credit is accepting
risk this layer cannot remove, and this document does not pretend
otherwise.

**What a broker owes.** A broker MUST retain terminal exchange records
for at least

```
max envelope spendable life  +  supported dispute/recovery window
```

The dispute window begins at **expiry**, not at the exchange, because
before expiry the question is premature. Retention shorter than that
deletes the evidence before anyone can ask — which the reference broker
did, evicting at 24h against an expiry window of roughly 38–57 hours.

Both inputs are deployment properties, not constants. `ticketValidityPeriod`
is a governance-settable parameter on the TicketBroker
(`setTicketValidityPeriod`), and round length is read from the
RoundsManager. An implementation that hardcodes either — as go-livepeer
and this one both did — is mirroring a value it does not control, which
is tolerable for deciding which of your own tickets are worth gas and is
NOT tolerable for telling a third party how long an envelope stays
spendable. A payer MUST derive `expires_after_round` from the value read
from the contract.

#### 5.3.1 Non-admission evidence

A broker MUST be able to state, in a signed record, that it never
admitted an exchange for a given request id. This is the evidence a
refund requires, and it is the only form of it available.

- **Retrievable by the consumer, keyed on the consumer's own
  `request_id`.** Never through the customer and never requiring a
  broker-minted job id: a customer that took the work and hid the receipt
  must not also be able to suppress the evidence against itself.
- **A distinct outcome, `NOT_ADMITTED`.** Not a zero-unit settlement — a
  settlement says an exchange happened and cost nothing, which is a
  different claim.
- **Signed with the same delegated key and canonicalization as
  settlements**, so a consumer verifies both with one code path. An
  unsigned non-admission record MUST NOT be emitted at all: unlike a
  settlement, which still carries useful accounting unsigned, its entire
  purpose is to be attributable.
- **Bound to** protocol, `request_id`, `work_id`, sender, recipient,
  quote identity, and broker identity, so it cannot be replayed against a
  different envelope carrying the same id from another payer.
- **Carries `observed_at` and a durable `coverage_started_at`.** A
  consumer MUST reject the record unless coverage began no later than its
  own job's issuance. A broker whose store was reset or reinitialized
  after issuance MUST NOT attest across the gap — absence there is
  forgetting, not non-admission.

  **The marker detects wiping, not rollback.** It lives in the store, so
  a fresh store re-stamps it and disqualifies itself. Restoring an older
  *backup* restores the older marker along with it, while dropping the
  job records written since — a broker in that state reports continuous
  coverage it does not have. `coverage_started_at` is therefore an
  **attributable broker assertion, not proof of uninterrupted storage**,
  and this document does not claim otherwise. A consumer wanting
  rollback resistance must anchor continuity outside the broker's own
  store: retaining periodic signed coverage statements works, because a
  rollback then contradicts evidence the consumer already holds.
- **Refused if any record exists** for the request — in flight, terminal,
  or pending accounting. Attesting mid-flight signs a statement the next
  second contradicts.
- **A consumer MUST NOT act on it before expiry.** Until then the request
  can still arrive and use the envelope.
- **Conflicting records MUST be retained.** A broker that has signed both
  a settlement and a non-admission for one request has produced two
  contradictory statements under one delegated key. That is the
  accountability the signature exists for, and discarding either destroys
  it.

## 6. What the broker never does

- Hold job state past the idempotency window — there is no job resource to
  poll; `GET`-shaped follow-ups (the old ABR status hack) belong to the
  workload's own surface, reachable via a descriptor-style coordinate in the
  response body if the capability wants one.
- Interpret payloads, rewrite bodies, or inject workload parameters.
- Treat its own claim as the buyer's bill.

## 7. Conformance obligations

Executable fixtures every broker implementation MUST pass:

- one unary, one stream, one multipart exchange against one *single*
  offering declaring all three transports;
- undeclared transport refused pre-payment;
- `Livepeer-Work-Units` present on success and on backend error (`0`), and
  delivered as a trailer with `Trailer` advertised on `stream`;
- unit echo matches the offering declaration;
- idempotency: same request id after terminal outcome replays the outcome
  (no second backend call, no second debit — verified by fault injection on
  a transiently failing debit); same id while in flight refused; same id
  with different body rejected;
- stream severed mid-body: extractor-measured units debited, no trailer
  emitted, subsequent retry with the same request id replays the recorded
  terminal outcome.

## Changelog

| Version | Date | Change |
|---|---|---|
| 1.0.12-draft | 2026-08-21 | §5.3 rewritten for consistency after LOC set policy. The earlier text said there was no unconditional expiry and then told consumers to finalize on expiry and re-encumber after a governance increase; those cannot both hold, and re-encumbrance is not implementable — finalized credit may already be spent. Replaced with four terminal outcomes (settle / unresolved / conservative full charge at an operational deadline / non-admission as audit evidence) and an explicit statement that automatic refund is unavailable. Records that `Livepeer-Request-Id` is not bound into `payment_bytes`, so a non-admission tombstone does not retire the envelope. Names the two protocol changes that would create refund authority. Adds `ticket_validity_period_observed_at` so a cached value is not mistaken for a current one. |
| 1.0.11-draft | 2026-08-21 | §5.3 corrected: expiry is CONDITIONAL, not unconditional. The TicketBroker evaluates `creationRound + ticketValidityPeriod > currRound` against current storage and keeps round block hashes permanently, so raising the governance parameter extends issued tickets and can revive lapsed ones. Payers now publish `ticket_validity_period` alongside the deadline so a consumer can detect the change, and `expires_after_round` is corrected to the last redeemable round (`creation_round + period - 1`) to match the contract's boundary rather than sitting one round beyond it. §5.3.1: `coverage_started_at` is described accurately as an attributable assertion — it detects a wiped store, not a restored backup. All raised by LOC. |
| 1.0.10-draft | 2026-08-21 | Add §5.3.1, non-admission evidence: a broker MUST be able to sign `NOT_ADMITTED` for a request id, retrievable by the consumer keyed on the consumer's own id, bound to protocol/request/work/sender/recipient/quote/broker, carrying `observed_at` and a durable `coverage_started_at`, refused if any record exists or if coverage began after issuance, and never emitted unsigned. States retention as a formula rather than a number, and requires `expires_after_round` to be derived from the contract's `ticketValidityPeriod` rather than a hardcoded mirror — it is governance-settable, and an understated value releases an encumbrance while the envelope is still spendable. Requirements from LOC. |
| 1.0.9-draft | 2026-08-21 | Add §5.3, encumbrance on an envelope that may never have been used. Separates FINALIZE (expiry is sufficient) from REFUND (it is not): a customer can submit an envelope, take the work, withhold the job id and settlement, wait for expiry and ask for the money back, and chain state cannot tell that apart from non-use because a losing ticket is never redeemed. Requires brokers to retain exchange records past the envelope's spendable life — the dispute window opens AT expiry, and the reference broker evicted at 24h against a 38–57h expiry, deleting the evidence before it could be asked for. Raised by LOC. |
| 1.0.8-draft | 2026-08-21 | §5.2 gains the debit-failure LIFECYCLE the OpenAI gateway team asked for: `202 accounting_pending` while a bounded, idempotent retry is active, a signed terminal settlement when the debit lands, and terminal `DEBIT_FAILED` only on retry exhaustion. Retry MUST reuse the original `debit_seq`, and a broker MUST NOT close a payee session while a debit against it is outstanding — a closed session refuses debits, so closing guarantees the retry can never land. Replaces settle-on-first-failure, which reported a recoverable timeout as a permanent loss. |
| 1.0.7-draft | 2026-08-21 | Add §4.1, replay semantics, and §5.2, the debit-failure rule. A replay returns the ACCOUNTING outcome and not the backend body — reproducing the body would make every broker a customer-data store — and the caller's loss is stated plainly rather than left implicit. A failed final debit MUST NOT be reported as a settled exchange: `debited_units` attests what the ledger took and the record carries `DEBIT_FAILED`. Both raised by the OpenAI gateway team. |
| 1.0.6-draft | 2026-08-21 | §3.2: a broker MUST NOT advertise a trailer on a response that cannot carry one, and MAY advertise `Livepeer-Settlement` on `stream`. The reference broker declared the settlement trailer on every exchange including Content-Length delimited unary ones, where the transport drops it silently — nothing asserted it, so a client waiting on the advertised name waited forever. |
| 1.0.5-draft | 2026-08-21 | Add §5.1: a paid-job settlement MUST carry the exchange's `request_id` alongside `job_id`, inside the signature. LOC reported that neither broker-minted `job_id` nor shared `work_id` binds a signed record to the clearinghouse's own durable job — the job path's counterpart to `gateway_session_id`. |
| 1.0.4-draft | 2026-08-21 | Add §4.6: a payment arriving on a payee-retired `work_id` MUST be refused rather than credited, and surfaced as `recipient_rotated` so a gateway re-mints instead of retrying a doomed envelope. The job spec never covered rotation, though the job path hits the same refusal; the payee obligation lives in paid-session §3.3.1. |
| 1.0.3-draft | 2026-08-20 | Add §4.5: a broker MUST refuse with `insufficient_balance` rather than run a backend for a session that cannot cover one work unit. A mainnet run served real work against a zero balance and reported success at every layer — the interim-debit ticker guards long-running work and is a no-op for a single exchange, so nothing checked. |
| 1.0.2-draft | 2026-08-20 | §3.2: the streamed terminal claim gains a portable channel. `GET /v1/settlement/{id}`, keyed by `Livepeer-Job-Id`, MUST serve the terminal units, unit name and signed settlement for the idempotency window; a running exchange answers 202 with state and an unknown id is never a zero claim. Trailers become an optimization rather than the only normative channel — Go reads them, HTTPX/Fetch/reqwest do not, and a claim no SDK can read forces a clearinghouse to bill zero or block. |
| 1.0.1-draft | 2026-08-20 | §4.4: spell out how the body hash is bound — envelope fingerprinted before the exchange, body digested as it streams, replay drained and compared. The requirement was already normative; the reference implementation had been binding the body's *length*, so a retry with a changed body of equal length received the recorded outcome. |
| 1.0.0-draft | 2026-08-18 | Initial protocol. Replaces `http-reqresp@v0`, `http-stream@v0`, `http-multipart@v0`; transports become per-request negotiation; idempotent open and a reliable claim channel become normative. |
