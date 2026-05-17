---
mode_name: http-stream
version: 0.1.0
status: accepted
spec_version: 0.1.0
last_updated: 2026-05-06
---

# Mode: `http-stream`

One HTTP request → **streaming response** (Server-Sent Events or HTTP chunked transfer
encoding). The streaming variant of [`http-reqresp`](./http-reqresp.md) — same path,
same request shape, same payment lifecycle — but the response body is incrementally
produced and `Livepeer-Work-Units` is reported as an HTTP **trailer**.

This file is a delta document. Where it does not say otherwise, all behavior is
identical to [`http-reqresp@v0`](./http-reqresp.md).

## When to use this mode

- OpenAI chat completions with `stream: true` (SSE).
- Any capability whose response is incrementally produced (token-by-token,
  frame-by-frame, progress updates) and where the client benefits from receiving
  partial output as it's generated.
- Backends that natively emit SSE or chunked responses.

## When NOT to use this mode

- One-shot request → complete response → use [`http-reqresp`](./http-reqresp.md).
- Multipart uploads → use `http-multipart`.
- Bidirectional → use `ws-realtime`.
- Long-lived sessions with separate media plane → use `session-control-plus-media`.

## Delta from `http-reqresp`

### Request — identical, except `Livepeer-Mode`

```
POST /v1/cap HTTP/1.1
Host: broker-a.orch.example.com
Livepeer-Capability: openai:chat-completions
Livepeer-Offering: vllm-h100-batch4
Livepeer-Payment: <base64-encoded payment envelope>
Livepeer-Spec-Version: 0.1
Livepeer-Mode: http-stream@v0
Livepeer-Request-Id: 550e8400-e29b-41d4-a716-446655440000
Content-Type: application/json
Accept: text/event-stream

{ ...capability-defined request body, opaque to the protocol... }
```

Same path (`POST /v1/cap`). Mode-distinguishing happens via the `Livepeer-Mode`
header, not the URL.

### Response (success) — streaming body + trailer

```
HTTP/1.1 200 OK
Content-Type: text/event-stream
Transfer-Encoding: chunked
Trailer: Livepeer-Work-Units
Livepeer-Request-Id: 550e8400-e29b-41d4-a716-446655440000

data: {"id":"...","choices":[{"delta":{"content":"Hello"}}]}

data: {"id":"...","choices":[{"delta":{"content":" world"}}]}

data: [DONE]

[trailers section]
Livepeer-Work-Units: 482
```

- The broker MUST declare `Trailer: Livepeer-Work-Units` in the response headers
  **before** sending any body bytes.
- The broker MUST emit `Livepeer-Work-Units: <actualUnits>` as an HTTP trailer after
  the final body byte and before closing the response.
- HTTP trailers require chunked transfer encoding (HTTP/1.1) or HTTP/2 — both are
  supported by the spec.
- The body is forwarded **unchanged** from the backend.
- Content-Type is set by the backend (typically `text/event-stream` for SSE; any
  chunked content type works).

**Why a trailer and not a final body event?** Mode-level uniformity —
`Livepeer-Work-Units` is the same canonical slot across all modes, regardless of
body format. A capability MAY also include usage info in its body (and OpenAI does,
in a final SSE event when `stream_options.include_usage: true`); the broker uses the
offering's declared extractor to compute units from there. The trailer is the
**protocol's authoritative slot** and MUST always be present on success.

### Response (error before stream starts)

Identical to `http-reqresp`: full HTTP error response with `Livepeer-Error`,
optional `Livepeer-Backoff`, structured JSON body. The trailer is not used for
pre-stream errors.

### Response (error during stream)

If the backend errors mid-stream:

- HTTP status remains `200 OK` (headers were already sent); the error is conveyed
  in the body using the capability's native error convention (e.g., a final SSE
  event of type `error`).
- Broker MUST still emit the `Livepeer-Work-Units` trailer with whatever
  partial-units count the extractor produced before the error.

If the gateway disconnects mid-stream:

- Broker SHOULD cancel the backend request and call
  `payment-daemon.Reconcile(<partial>)` + `CloseSession()` server-side.
- The trailer cannot be delivered (the connection is gone) — the gateway falls
  back to the estimate for billing; payment-daemon's session-close logic refunds
  any unconsumed estimate against the customer's next session.

## Payment lifecycle

Same shape as `http-reqresp`: estimate → debit-up-front → Serve → reconcile → close.
The only difference is **when** `actualUnits` becomes known: at end-of-stream rather
than end-of-request-body. The reconcile step happens after the trailer is emitted.

For **very long** streams (e.g., a 10-minute live transcription), implementations MAY
add interim debits at a configured cadence — but the v0.1 default is single-debit +
post-stream reconcile, identical to `http-reqresp`. Interim debits are a v2
consideration.

## Forwarding behavior

The broker, in addition to the `http-reqresp` rules:

- Adds `Trailer: Livepeer-Work-Units` to the outbound response headers before
  emitting any body bytes.
- Flushes response chunks promptly as backend bytes arrive (no buffering — SSE is
  latency-sensitive).
- Accumulates partial-units state via the offering's declared extractor as bytes
  flow.
- Emits `Livepeer-Work-Units` as the trailer after the last body byte.

The gateway:

- Sets `Accept` per the capability's content type (typically `text/event-stream`
  for OpenAI-shaped streams; backend MAY ignore `Accept`).
- Reads response chunks/events and forwards (or buffers) per its customer-facing
  protocol.
- Reads the `Livepeer-Work-Units` **trailer** when the response stream ends; uses
  it for the customer USD ledger debit.
- Falls back to `expected_max_units` if the trailer is absent (mid-stream
  disconnect).

## Timeouts

- Total request timeout: same advisory pattern as `http-reqresp`.
- **Inactivity timeout**: gateway and broker SHOULD detect a stalled stream (no
  body bytes for N seconds; recommended default 30s) and close the connection.
  Stalled stream is treated as gateway disconnect for accounting.

## Body size

- Mode does not impose a hard cap on cumulative body size. SSE streams can be
  arbitrarily long.
- Implementations SHOULD apply a hard cap (e.g., 100 MB or 30 minutes elapsed) and
  document it via `extra.max_response_bytes` / `extra.max_stream_seconds`.

## Idempotency

Same as `http-reqresp`: not promised; capability's choice.

## Observability

In addition to `http-reqresp` metrics:

- `livepeer_mode_stream_duration_seconds{mode="http-stream",capability,offering}` —
  histogram (full-stream wall time, headers-to-trailer).
- `livepeer_mode_stream_first_byte_seconds{mode="http-stream",capability,offering}` —
  histogram (TTFB; latency-relevant for SSE).
- `livepeer_mode_stream_bytes_total{mode="http-stream",capability,offering,direction="response"}` —
  counter.

## Versioning

Per-mode SemVer. Currently `0.1.0`.

## Conformance

Tests, at minimum:

- Happy path: stream completes; `Livepeer-Work-Units` trailer emitted with the
  expected value.
- `Trailer: Livepeer-Work-Units` is declared in response headers before any body
  byte is sent.
- Mid-stream backend error: 200 + body-level error event + trailer still emitted
  with partial count.
- Gateway disconnect mid-stream: broker cancels backend, closes session
  server-side; no trailer emitted (connection is gone).
- Pre-stream errors (header validation, payment validation, capacity exhausted):
  full HTTP error response per `http-reqresp` rules.
- Header strip + backend-auth-injection: same as `http-reqresp`.

Fixtures: `conformance/fixtures/http-stream/*.yaml`.

## Changelog

| Mode version | Date | Change |
|---|---|---|
| 0.1.0 | 2026-05-06 | Initial draft. |
