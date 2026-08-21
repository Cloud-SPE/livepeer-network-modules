---
spec_name: paid-job
version: 1.0.6-draft
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

**Funded ceiling (streams).** A `stream` exchange runs against its envelope's
funded ceiling: the broker MAY terminate the stream once extractor-measured
usage reaches it, ending the body cleanly and claiming exactly the delivered
units in the trailer. This is the seller's fail-closed protection for
long-running exchanges — there is no mid-job refill, deliberately. A workload
that legitimately needs mid-exchange funding is `paid-session/v1` work.

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
| 1.0.6-draft | 2026-08-21 | §3.2: a broker MUST NOT advertise a trailer on a response that cannot carry one, and MAY advertise `Livepeer-Settlement` on `stream`. The reference broker declared the settlement trailer on every exchange including Content-Length delimited unary ones, where the transport drops it silently — nothing asserted it, so a client waiting on the advertised name waited forever. |
| 1.0.5-draft | 2026-08-21 | Add §5.1: a paid-job settlement MUST carry the exchange's `request_id` alongside `job_id`, inside the signature. LOC reported that neither broker-minted `job_id` nor shared `work_id` binds a signed record to the clearinghouse's own durable job — the job path's counterpart to `gateway_session_id`. |
| 1.0.4-draft | 2026-08-21 | Add §4.6: a payment arriving on a payee-retired `work_id` MUST be refused rather than credited, and surfaced as `recipient_rotated` so a gateway re-mints instead of retrying a doomed envelope. The job spec never covered rotation, though the job path hits the same refusal; the payee obligation lives in paid-session §3.3.1. |
| 1.0.3-draft | 2026-08-20 | Add §4.5: a broker MUST refuse with `insufficient_balance` rather than run a backend for a session that cannot cover one work unit. A mainnet run served real work against a zero balance and reported success at every layer — the interim-debit ticker guards long-running work and is a no-op for a single exchange, so nothing checked. |
| 1.0.2-draft | 2026-08-20 | §3.2: the streamed terminal claim gains a portable channel. `GET /v1/settlement/{id}`, keyed by `Livepeer-Job-Id`, MUST serve the terminal units, unit name and signed settlement for the idempotency window; a running exchange answers 202 with state and an unknown id is never a zero claim. Trailers become an optimization rather than the only normative channel — Go reads them, HTTPX/Fetch/reqwest do not, and a claim no SDK can read forces a clearinghouse to bill zero or block. |
| 1.0.1-draft | 2026-08-20 | §4.4: spell out how the body hash is bound — envelope fingerprinted before the exchange, body digested as it streams, replay drained and compared. The requirement was already normative; the reference implementation had been binding the body's *length*, so a retry with a changed body of equal length received the recorded outcome. |
| 1.0.0-draft | 2026-08-18 | Initial protocol. Replaces `http-reqresp@v0`, `http-stream@v0`, `http-multipart@v0`; transports become per-request negotiation; idempotent open and a reliable claim channel become normative. |
