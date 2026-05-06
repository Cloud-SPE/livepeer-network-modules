---
mode_name: http-reqresp
version: 0.1.0
status: accepted
spec_version: 0.1.0
last_updated: 2026-05-06
---

# Mode: `http-reqresp`

One HTTP request → one HTTP response. The simplest interaction shape; the template the
other five modes deviate from.

## When to use this mode

- OpenAI-compatible non-streaming endpoints (`/v1/embeddings`,
  `/v1/images/generations`, `/v1/images/edits`).
- Custom REST endpoints that complete in a single response.
- Anything where the response body is fully known before the response begins.

## When NOT to use this mode

- Streaming responses (SSE, chunked) → use `http-stream`.
- Multipart uploads → use `http-multipart`.
- Bidirectional WebSocket → use `ws-realtime`.
- Long-lived sessions with media planes → use `session-control-plus-media`.
- RTMP ingress / HLS egress → use `rtmp-ingress-hls-egress`.

## Wire shape

### Path

The broker exposes a **single canonical path** for this mode: `POST /v1/cap`. The
capability is identified by the `Livepeer-Capability` header, not the URL. (Single
path keeps capability IDs URL-encoding-free; the header is the system of record.)

### Request

```
POST /v1/cap HTTP/1.1
Host: broker-a.orch.example.com
Livepeer-Capability: openai:embeddings:bge-large
Livepeer-Offering: vllm-h100
Livepeer-Payment: <base64-encoded payment envelope>
Livepeer-Spec-Version: 0.1
Livepeer-Mode: http-reqresp@v0
Livepeer-Request-Id: 550e8400-e29b-41d4-a716-446655440000
Content-Type: application/json

{ ...capability-defined request body, opaque to the protocol... }
```

- All five required Livepeer-* request headers per
  [`../headers/livepeer-headers.md`](../headers/livepeer-headers.md).
- Body is **opaque to the protocol** — the capability defines its shape.
- `Content-Type` is application-defined (typically `application/json` for
  OpenAI-shaped bodies; any value is acceptable).

### Response (success)

```
HTTP/1.1 200 OK
Livepeer-Work-Units: 482
Livepeer-Request-Id: 550e8400-e29b-41d4-a716-446655440000
Content-Type: application/json

{ ...backend response body, returned unchanged... }
```

- `Livepeer-Work-Units` MUST be set on success. Value is a non-negative integer in
  the unit declared by the offering's `work_unit.name`.
- `Livepeer-Request-Id` SHOULD be echoed back if it was set on the request.
- Body is returned **unchanged** from the backend.
- Any 2xx status the backend returns is passed through (200, 201, 202, 204).

### Response (error)

Per the headers spec error codes. Examples:

```
HTTP/1.1 503 Service Unavailable
Livepeer-Backoff: 30
Livepeer-Error: capacity_exhausted
Content-Type: application/json

{ "error": "capacity_exhausted", "message": "broker has no slots", "request_id": "550e8400-..." }
```

```
HTTP/1.1 401 Unauthorized
Livepeer-Error: payment_invalid
Content-Type: application/json

{ "error": "payment_invalid", "message": "ticket failed face-value check", "request_id": "550e8400-..." }
```

## Payment lifecycle

Single-debit with post-Serve reconciliation:

1. **Gateway estimates** `expected_max_units` (upper bound) for this request based on
   its workload knowledge.
2. **Gateway** includes `expected_max_units` in the `Livepeer-Payment` envelope.
3. **Broker validates** the ticket; `payment-daemon` (receiver) opens a session.
4. **Broker debits** `expected_max_units` up front against the session balance.
5. **Broker forwards** the (Livepeer-stripped, backend-auth-injected) request to the
   backend; awaits the response.
6. **Broker computes** `actualUnits` from the backend's response via the offering's
   declared extractor (a host-config concern; see
   [`../extractors/`](../extractors/)).
7. **Broker reports** `Livepeer-Work-Units: <actualUnits>` in the response headers.
8. **Broker calls** `payment-daemon.Reconcile(actualUnits)` — refunds the difference
   when actual was less than estimated.
9. **Broker calls** `payment-daemon.CloseSession()`.

Steps 3–9 are per-request; no long-lived session state. Each request opens and closes
its own session.

### Backend error handling

| Backend outcome | Broker response | `Livepeer-Work-Units` | Reconcile |
|---|---|---|---|
| 2xx | passthrough | extractor-computed | refund (estimate - actual) if positive |
| 4xx (caller error) | passthrough status + `Livepeer-Error: backend_unavailable` is **not** set (it's the caller's error) | extractor-computed (may be 0 if no work consumed) | refund (estimate - actual) |
| 5xx or timeout | 502 + `Livepeer-Error: backend_unavailable` | 0 | full refund (estimate) |
| Gateway disconnects mid-flight | aborted | last computed value or 0 | refund (estimate - reported) |

## Forwarding behavior

### Broker

1. Validate payment + headers; reject on mismatch per the headers spec error codes.
2. **Strip all `Livepeer-*` headers** from the inbound request.
3. **Inject backend-specific auth** as declared in `host-config.yaml` (e.g.,
   `Authorization: Bearer <vault-resolved-secret>` for third-party API resale).
4. Issue the outbound request to `backend.url` with the body **unchanged**.
5. Await the backend's response.
6. Compute `actualUnits`; set `Livepeer-Work-Units`; echo `Livepeer-Request-Id`;
   set `Livepeer-Error`/`Livepeer-Backoff` if applicable.
7. Return the backend body **unchanged** with the augmented headers.

### Gateway

1. Receive a customer request via the gateway's customer-facing protocol.
2. Select a route via `Resolver.Select(capability_id, offering_id, ...)`.
3. Estimate `expected_max_units` from the request shape (workload-specific).
4. Build the `Livepeer-Payment` envelope through `payment-daemon` (sender).
5. Set the five required Livepeer-* request headers + optional
   `Livepeer-Request-Id`.
6. Issue `POST <worker_url>/v1/cap` with the body unchanged.
7. On 2xx: return the body to the customer; read `Livepeer-Work-Units` for the
   customer USD ledger debit.
8. On 503 + `Livepeer-Backoff`: route to a different orch; mark this orch+capability
   as backed-off for at least the advertised seconds.
9. On other errors: map to the gateway's own customer-facing error model.

## Timeouts

- The mode does not impose a hard timeout. Each side picks based on the capability's
  workload (a chat completion may take 30 s; an embedding 1 s).
- Gateway SHOULD set a request timeout appropriate to the capability. The offering's
  `extra` field MAY advertise a recommended timeout (e.g.,
  `extra.timeout_seconds: 60`).
- Broker SHOULD set a backend timeout no longer than the gateway's overall timeout
  minus a small buffer.
- Cancellation: gateway closes the connection → broker SHOULD cancel the backend
  request and report `Livepeer-Work-Units` reflecting whatever was consumed (often
  0).

## Body size

- The mode does not impose a hard limit. Implementations SHOULD apply reasonable
  defaults and document them via `extra.max_request_bytes` / `extra.max_response_bytes`
  in the offering, when relevant.
- Recommended defaults (not normative): 25 MiB request, 50 MiB response.

## Idempotency

- The mode does not promise idempotency. Capabilities that are naturally idempotent
  (e.g., embeddings of the same input) MAY treat `Livepeer-Request-Id` as an
  application-layer idempotency key; the protocol does not enforce this.

## Observability

The broker SHOULD expose Prometheus metrics for this mode. Suggested names (per the
[`../metrics/`](../metrics/) spec, TBD):

- `livepeer_mode_requests_total{mode="http-reqresp",capability,offering,outcome}` — counter.
- `livepeer_mode_request_duration_seconds{mode="http-reqresp",capability,offering}` — histogram.
- `livepeer_mode_work_units_total{mode="http-reqresp",capability,offering}` — counter (sum of `actualUnits`).
- `livepeer_mode_estimate_overshoot_units{mode="http-reqresp",capability,offering}` — histogram (`expected_max_units − actualUnits`, for tuning gateway estimates).

Demand visibility is fed by these surfaces (see
[core belief #1 / requirement R10](../../docs/design-docs/core-beliefs.md)).

## Versioning

This mode follows per-mode SemVer (per spec-wide hybrid SemVer).

- `0.x.y` — pre-1.0; minor bumps may break.
- `1.0.0` — first stable release.
- Major bumps require deprecation notice in this mode's changelog and the
  spec-wide changelog.

## Conformance

The conformance suite tests, at minimum:

- Happy path: 2xx response with `Livepeer-Work-Units` set and reconciled.
- Header validation: each missing/mismatched required header produces the expected
  `Livepeer-Error` code.
- 503 + `Livepeer-Backoff` round-trip when broker capacity is exhausted.
- Backend 5xx or timeout → broker returns 502 + `backend_unavailable` + full refund.
- Forwarding: broker strips `Livepeer-*` and injects declared backend auth.
- Reconciliation: `actualUnits ≤ expected_max_units` always; refund triggered when
  `actual < estimate`.

Fixtures live under `conformance/fixtures/http-reqresp/*.yaml`.

## Changelog

| Mode version | Date | Change |
|---|---|---|
| 0.1.0 | 2026-05-06 | Initial draft. |
