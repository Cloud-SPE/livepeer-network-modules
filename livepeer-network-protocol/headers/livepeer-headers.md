---
status: draft (rewritten for the v1 protocols)
spec_version: 1.0.7-draft
last_updated: 2026-08-20
---

# Livepeer wire headers

This document defines the `Livepeer-*` HTTP header conventions used between gateway,
broker, and (where relevant) the gateway resolver. Both protocol specs
([`paid-job/v1`](../protocols/paid-job.md), [`paid-session/v1`](../protocols/paid-session.md))
depend on this document.

## Audience and scope

Implementers of:

- **Gateway middleware** — sets request headers; reads response headers.
- **Capability broker** — reads request headers, validates payment, sets response
  headers.
- **payment-daemon** (sender + receiver) — owns the `Livepeer-Payment` envelope.

Out of scope:

- Customer-facing auth (`Authorization: Bearer <api-key>`) — gateway-internal concern.
- Backend-facing auth — broker concern (e.g., the orch's OpenAI API key for resale);
  declared in `host-config.yaml`, not on the wire to the gateway.

## Header taxonomy

| Header | Direction | Required | Set by | Read by |
|---|---|---|---|---|
| `Livepeer-Capability` | request → broker | yes | gateway | broker, payment-daemon |
| `Livepeer-Offering` | request → broker | yes | gateway | broker, payment-daemon |
| `Livepeer-Payment` | request → broker | yes | gateway (via payment-daemon sender) | broker (via payment-daemon receiver) |
| `Livepeer-Protocol` | request → broker | yes | gateway | broker |
| `Livepeer-Request-Id` | request → broker | yes | gateway | broker (idempotency key; echoed back) |
| `Livepeer-Backoff` | response from broker | when 503 | broker | gateway |
| `Livepeer-Work-Units` | response from broker | every terminal paid-job response | broker | gateway |
| `Livepeer-Work-Unit` | response from broker | with `Livepeer-Work-Units` | broker | gateway |
| `Livepeer-Job-Id` | response from broker | every terminal paid-job response | broker | gateway |
| `Livepeer-Health-Status` | response on `/registry/health` | yes (on that path) | broker | gateway resolver |
| `Livepeer-Settlement` | response from broker | when applicable | broker | gateway |
| `Livepeer-Error` | response from broker on error | when error | broker | gateway |

## Header reference

### `Livepeer-Capability`

The capability identifier this request is paying for.

- **Value:** opaque UTF-8 string from the orch's manifest.
- **Example:** `Livepeer-Capability: openai:chat-completions`
- The broker MUST reject (404 + `Livepeer-Error: capability_not_served`) any request
  whose `Livepeer-Capability` is not in the orch's currently-published
  `/registry/offerings`.

### `Livepeer-Offering`

The offering identifier under the capability — disambiguates when a capability has
multiple priced tiers (different models, different SLA tiers, different hardware).

- **Value:** opaque UTF-8 string from the orch's manifest.
- **Example:** `Livepeer-Offering: vllm-h100-batch4`
- The broker MUST reject (404 + `Livepeer-Error: offering_not_served`) any request
  whose `(Livepeer-Capability, Livepeer-Offering)` pair is not currently served.

### `Livepeer-Payment`

The payment envelope. Base64-encoded protobuf message
(`livepeer.payments.v1.Payment`).

The envelope is the canonical wire-format `livepeer.payments.v1.Payment`.

It carries:

- payee-issued `ticket_params`
- sender identity
- per-ticket sender params
- `expected_price` — the payer's accepted unit-pricing basis for the funded work

Behavior:

- Mismatch between the request headers and the route the broker resolves for the
  validated payment → broker rejects (401 + `Livepeer-Error: payment_envelope_mismatch`).
- Failed ticket validation (signature, replay, insufficient face value) → 401 +
  `Livepeer-Error: payment_invalid`.
- The envelope's wire shape is owned by `payment-daemon`; the protobuf definition
  lives there. This document references it; do not duplicate.

### `Livepeer-Protocol`

The protocol + major version the gateway is speaking. Replaces the pre-v1
`Livepeer-Mode` and `Livepeer-Spec-Version` pair: the protocol tag carries its
own version, and there is no separate spec-wide wire version to negotiate.

- **Value:** `<name>/v<major>` — currently `paid-job/v1` or `paid-session/v1`.
- **Example:** `Livepeer-Protocol: paid-job/v1`
- The broker MUST reject (505 + `Livepeer-Error: protocol_unsupported`) if it
  does not implement that protocol + major version for the named capability.
- Why this is a header (not just derived from the manifest): self-describing
  requests survive intermediaries, simplify logs, and let the broker fast-fail
  before unpacking the payment envelope.

### `Livepeer-Request-Id`

Required. UUID (or any opaque string ≤64 chars). The idempotency key for the
exchange: paid-job §4 and paid-session §3.1 define replay semantics on it —
a retried request converges on the recorded outcome, and reuse with different
content is `request_id_reuse`.

- **Example:** `Livepeer-Request-Id: 550e8400-e29b-41d4-a716-446655440000`
- The broker MUST echo the value in response headers and SHOULD emit it in
  logs and metrics labels.

### `Livepeer-Rebind-From`

Optional, `paid-session/v1` top-up only. Declares a recipient rotation: the
value is the `work_id` the session is moving **off**. Present only on the
retry after a `recipient_rotated` refusal.

A rebind is declared, never inferred from a payment whose identity differs
from the session's — see paid-session §3.3.1 for the three rules a broker
verifies before moving a session, and for why inference is unsafe.

- **Example:** `Livepeer-Rebind-From: b3d1f0…c47a`
- The control-WS mirror carries the same value as `rebind_from` in the
  `session.topup` frame, since a frame has no headers.

### `Livepeer-Backoff`

On 503, the broker advises the gateway how long to back off before retrying or
selecting another orch.

- **Value:** integer seconds.
- **Example:** `Livepeer-Backoff: 30`
- REQUIRED when the response is 503.
- Gateway resolver SHOULD treat the orch+capability as unavailable for at least
  that many seconds.
- `0` is permitted ("retry immediately, transient capacity blip").

### `Livepeer-Work-Units`

In responses where work has been performed, the broker reports the actual units
consumed.

- **Value:** integer (interpreted in the unit declared by the offering's
  `work_unit.name`).
- **Example:** `Livepeer-Work-Units: 1247` (e.g., 1247 tokens).
- Set on **every** terminal `paid-job` response, including errors (`0` when no
  billable work occurred); delivered as an HTTP trailer (with `Trailer`
  advertised) on the `stream` transport. See paid-job §3.2.
- `paid-session` usage travels as cumulative claims on runner events and the
  control plane, not in this header.
- This is the seller's usage claim under the dual-meter trust model — consumed
  for runway accounting and divergence detection, never as the buyer's bill.

### `Livepeer-Work-Unit`

Echo of the offering's declared work-unit name, sent alongside
`Livepeer-Work-Units` so gateways can reject unit drift locally.

- **Example:** `Livepeer-Work-Unit: tokens`

### `Livepeer-Job-Id`

Broker-assigned identifier for one paid-job exchange — the audit key joining
claim, debit, and idempotency record.

- **Example:** `Livepeer-Job-Id: job_01jx6f6w0rpk`

### `Livepeer-Settlement`

Broker-authoritative settlement record for the completed request or session window.

- **Value:** base64-encoded JSON envelope:

  ```json
  {
    "payload": { … the settlement record … },
    "signature": {
      "algorithm": "secp256k1",
      "canonicalization": "jcs",
      "value": "0x…130 hex…"
    }
  }
  ```

- `payload` is the JCS-canonical (RFC 8785) JSON form of
  `livepeer.payments.v1.SettlementRecord`, using the proto field names. It is
  already canonical as transmitted, so a verifier signs and checks exactly the
  bytes it received.
- The signature is EIP-191 personal-sign over those bytes — the same scheme as
  the manifest, so a consumer verifies settlement with primitives it already
  has rather than a second scheme.
- **The signer is a delegated hot key, not the orch's cold key.** A broker is
  network-exposed; compromising one MUST NOT cost an operator the identity that
  anchors it on-chain. The cold key delegates by publishing the hot key in the
  manifest's `settlement_keys`, and resolvers project every currently-valid key
  onto the route, so key discovery and rotation ride the path a consumer
  already trusts.
- A consumer MUST accept a record signed by any delegated key whose validity
  window contains the record's `issued_at` — an outgoing key stays published
  until its `expires_at` so a record signed just before a rotation still
  verifies.
- **`signature` is absent when the broker holds no delegated key.** A consumer
  that needs integrity MUST reject an unsigned envelope; the field is omitted
  rather than emptied so the distinction cannot be missed.
- For `paid-job/v1`, retrievable from `GET /v1/settlement/{id}` keyed by
  `Livepeer-Job-Id` — always, and on every transport, because a trailer is
  unreadable in most SDK stacks (paid-job §3.2). In band it may additionally
  arrive as a trailer on a **streamed** exchange, where headers commit long
  before the units are known.
- **A trailer MUST NOT be advertised on a response that cannot carry one.** A
  trailer rides only on a chunked response, so a `Content-Length` delimited
  (unary) exchange that names `Livepeer-Settlement` in its `Trailer` header is
  telling a client to wait for something the transport will silently drop. On
  that transport the record is retrieved from the query surface instead.
- For `paid-session/v1`, emitted on the terminal response, and retrievable at
  any time from `GET /v1/settlement/{id}` — by `session_id`, by
  `gateway_session_id`, or by any `work_id` the session has held, including one
  a rotation superseded. A settlement delivered once, through a channel that
  can drop it, is not one a clearinghouse can rely on.
- **`gateway_session_id` MUST resolve.** It is the only one of those keys a
  clearinghouse issued itself; `session_id` is broker-local and reaches it
  through the customer-controlled SDK. A broker MUST therefore keep it unique
  across retained sessions and refuse a colliding open with
  `gateway_session_id_reuse` (409).
- **A key matching several sessions MUST fail, not guess.** One `work_id` can
  cover many sessions, so a query by it MUST answer `ambiguous_identifier`
  (409) rather than return one of them: a correctly signed record for the
  wrong session is indistinguishable, from the record alone, from the right
  one. The error names a key that resolves.

For `paid-session/v1` the record additionally carries the session's identity
chain and cumulative accounting (paid-session §3.3.1): stable `session_id`,
current and predecessor `work_id`, `rotation_generation`, cumulative claimed and
debited units with per-generation subtotals, and `settlement_seq` — monotonic
per session, so `(session_id, settlement_seq)` is the replay binding.

`debited_units` is the authoritative billing quantity: `claimed_units` is what a
runner asserted, `debited_units` is what the ledger moved.
`billed_value_wei` is one ceiling over the cumulative total
(`offering-axes.md` §6.1), and `amount_wei` / `per_units` travel with it so a
consumer can recompute rather than trust.

Gateways and clearinghouses should store the envelope as the authoritative
settlement record rather than re-deriving settlement from local heuristics.

### `Livepeer-Health-Status`

Set by the broker on responses to `GET /registry/health`. Indicates the orch's live
capacity status for each currently-served capability.

- **Value:** a JSON object literal (URL-encoded if the JSON contains commas, per
  RFC 7230 header-value rules).
- **Example:**
  ```
  Livepeer-Health-Status: {"openai:chat-completions":"available","video:transcode.live":"saturated"}
  ```
- Each value MUST be one of: `"available" | "saturated" | "draining" | "down"`.
- Gateways poll this every 15-30 seconds. The full three-layer health model lives
  in [`backend-health.md`](../../docs/design-docs/backend-health.md).
- Alternative: place the JSON in the response body. Header form is preferred for
  consistency with the `Livepeer-*` family and to allow `HEAD` checks.

### `Livepeer-Error`

On any non-2xx response, the broker SHOULD set a machine-readable error code.

- **Value:** one of the codes in [Error codes](#error-codes) below.
- The response body SHOULD also include a JSON object with structured error info
  (see [Error body](#error-body)).
- For in-flight terminations (a `stream` transport body, an `inband-ws`
  session), the response is already flowing when the broker decides to
  terminate. Broker emits the error
  code as an HTTP trailer where the wire allows (`Trailer: Livepeer-Error`
  + the value when the body is complete) or as the WebSocket close
  reason. `insufficient_balance` is the canonical code for these
  mid-flight terminations (plan 0015).

## Error codes

| Code | HTTP status | Meaning |
|---|---|---|
| `capability_not_served` | 404 | The orch does not currently advertise this `Livepeer-Capability`. |
| `offering_not_served` | 404 | The capability is served but the requested offering is not. |
| `payment_envelope_mismatch` | 401 | `Livepeer-Payment` envelope contents disagree with header values. |
| `payment_invalid` | 401 | Ticket failed validation (signature, replay, insufficient face value). |
| `protocol_unsupported` | 505 | Broker does not implement the requested `Livepeer-Protocol` for this capability. |
| `protocol_transport_unsupported` | 400 | The request selected a transport the offering does not declare (paid-job §2). |
| `job_in_flight` | 409 | Retry of a request id whose original exchange is still executing (paid-job §4). Retryable. |
| `request_id_reuse` | 400 | Request id replayed with different capability, offering, envelope, or body (paid-job §4; paid-session §3.1 opens and §3.3 top-ups). |
| `gateway_session_id_reuse` | 409 | Session open declared a `gateway_session_id` already bound to a retained session. The id is the settlement query's only consumer-issued key, so it must resolve to exactly one session; accepting a duplicate breaks the lookup for both. Choose an unused id — no retry of the same open succeeds. |
| `accounting_pending` | 202 | The exchange was delivered but its debit has not landed and is being retried. Distinct from a job still running: nothing further is expected from the backend, only from the ledger. It will reach a terminal settlement — signed once the debit lands, or `DEBIT_FAILED` on retry exhaustion (paid-job §5.2). Hold the encumbrance; do not book or write off. |
| `ambiguous_identifier` | 409 | A settlement query key matches more than one session — a `work_id` shared across sessions on one ticket session. Returning one would be a valid signature for the wrong session. Re-query by `gateway_session_id` or `session_id`. |
| `refill_refused` | 409 | Top-up refused; `will_refuse_next_refill` was advertised beforehand (paid-session §3.3). |
| `recipient_rotated` | 409 | The payee rotated its recipient rand, so every ticket in the batch was rejected. Mechanical remedy: re-fetch ticket params, re-mint, retry — for a session, declaring `Livepeer-Rebind-From` (paid-session §3.3.1). |
| `rebind_refused` | 409 | A declared rotation rebind the broker would not perform: wrong predecessor, a successor that did not credit, a different sender, or a rotation bound reached (paid-session §3.3.1). |
| `backend_unavailable` | 502 | Backend reachable but returned an error the broker can't recover from. |
| `capacity_exhausted` | 503 | Broker has no slots; see `Livepeer-Backoff`. |
| `insufficient_balance` | 402 | The payer's balance does not cover the work. Emitted **before** the backend runs when the credited balance cannot cover one work unit (paid-job §4.5), and mid-flight when a long-running session's runway runs out. The header is emitted as a trailer where the protocol allows it (the response body has typically already begun); the connection is closed by the broker. Plan 0015. |
| `internal_error` | 500 | Anything else. |

Workload-specific failures are **not** protocol error codes. The mode-era
`ffmpeg_subprocess_failed`, `rtmp_ingest_idle_timeout`, and
`backpressure_drop` codes were removed with the v0 modes and the broker-hosted
media plane: under `paid-session/v1` a runtime that dies is a runner fact,
surfaced as `session.failed` with a runner-authored `close_reason` (§7.2), and
never as a `Livepeer-Error` value the gateway has to know per workload.

### Error body

Error responses SHOULD include a JSON body with at minimum:

```json
{
  "error": "<code>",
  "message": "<human-readable description>",
  "request_id": "<from Livepeer-Request-Id, generated if absent>"
}
```

## Header ordering and case

- HTTP headers are case-insensitive (RFC 7230). Implementations SHOULD emit the
  canonical mixed-case form (`Livepeer-Capability`) and accept any case on read.
- No required ordering. The five required request headers (`Livepeer-Capability`,
  `Livepeer-Offering`, `Livepeer-Payment`, `Livepeer-Protocol`,
  `Livepeer-Request-Id`) MUST all be present on any paid request.

## Forwarding behavior (broker → backend)

The broker is a transparent proxy with the following obligations:

- **Strip all `Livepeer-*` headers** before forwarding to the backend. The backend
  MUST NOT see Livepeer protocol headers.
- **Inject backend-specific auth** when declared in `host-config.yaml`. For
  example: `Authorization: Bearer <vault-resolved-secret>` when reselling a
  third-party API.
- **Pass through application-level headers** (`Content-Type`, `Accept`,
  `User-Agent`, etc.) at the implementer's discretion. The protocol specs MAY
  further constrain this.
- **Echo `Livepeer-Request-Id` in logs**, even though it's stripped from the
  outbound request.

## Conformance

The conformance suite ([`../conformance/`](../conformance/), `make conformance`)
verifies, at minimum:

- All required request headers parsed correctly.
- All header/envelope mismatch paths produce the right `Livepeer-Error` codes.
- 503 + `Livepeer-Backoff` round-trip behavior.
- `Livepeer-Work-Units` post-`Serve` accounting.
- `Livepeer-Protocol` rejection on unsupported values.
- Forwarding behavior — broker strips `Livepeer-*` and injects declared backend
  auth.

See [`../conformance/`](../conformance/).

## Changelog

| Spec version | Change |
|---|---|
| 0.1.0 | Initial draft. |
| 0.1.1 | Add `insufficient_balance` error code for long-running sessions terminated by the broker mid-flight (plan 0015). Pre-1.0 minor additions are non-breaking; receivers continue to validate the major version only. |
| 0.1.2 | Add `ffmpeg_subprocess_failed` and `rtmp_ingest_idle_timeout` error codes for `rtmp-ingress-hls-egress` (plan 0011-followup). Pre-1.0 minor additions are non-breaking. |
| 0.1.3 | Add `backpressure_drop` error code for the `session-control-plus-media` control-WebSocket (plan 0012-followup). Pre-1.0 minor additions are non-breaking. |
| 1.0.7-draft | Add `accounting_pending` (202): a delivered exchange whose debit is still being retried, distinct from one still running. |
| 1.0.6-draft | A `Livepeer-Settlement` trailer MUST NOT be advertised on a response that cannot carry one. Trailers ride only on chunked responses, and the reference broker declared one on every paid-job exchange including Content-Length delimited unary ones, where net/http dropped it silently — a client that waits for the advertised name waits forever. States the per-transport rule: the query surface always, the trailer additionally on streamed. |
| 1.0.5-draft | `GET /v1/settlement/{id}` MUST also resolve `gateway_session_id`, the only lookup key a clearinghouse issues itself, and brokers MUST keep it unique across retained sessions (`gateway_session_id_reuse`). A key matching several sessions MUST answer `ambiguous_identifier` rather than return one of them. LOC could reject a wrong-session record but had no key that would find the right one. |
| 1.0.4-draft | `insufficient_balance` is also a pre-flight refusal, not only a mid-flight termination (paid-job §4.5). |
| 1.0.3-draft | `GET /v1/settlement/{id}` also serves paid-job exchanges, keyed by `Livepeer-Job-Id`, so a streamed claim is reachable by clients that cannot read HTTP trailers. |
| 1.0.2-draft | `Livepeer-Settlement` becomes a signed JSON envelope for BOTH protocols: JCS-canonical payload plus an EIP-191 secp256k1 signature from a manifest-delegated hot key, with the signature omitted when a broker holds no delegation. Adds the paid-session identity chain and cumulative accounting to the record, and names `GET /v1/settlement/{id}` as the retrieval path. Replaces the bare base64 protobuf — the channel that carried it ends at a customer-controlled SDK, so integrity has to travel with the record. |
| 1.0.1-draft | Add `recipient_rotated` and `rebind_refused` for recipient rotation, and the `Livepeer-Rebind-From` request header that declares a rebind's predecessor (paid-session §3.3.1). Pre-1.0-style minor addition: receivers validate the major only. |
| 1.0.0-draft | **Breaking.** Rewritten for the v1 protocols (2026-08-19). `Livepeer-Mode` + `Livepeer-Spec-Version` replaced by `Livepeer-Protocol`; `Livepeer-Request-Id` becomes required (it is the idempotency key); `Livepeer-Work-Unit` and `Livepeer-Job-Id` added; `protocol_unsupported`, `protocol_transport_unsupported`, `job_in_flight`, `request_id_reuse`, and `refill_refused` added. The mode-era `ffmpeg_subprocess_failed`, `rtmp_ingest_idle_timeout`, and `backpressure_drop` codes removed with the broker-hosted media plane. |
