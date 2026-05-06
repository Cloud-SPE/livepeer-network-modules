---
status: accepted (plan 0002 Q5 closed 2026-05-06)
spec_version: 0.1.0
last_updated: 2026-05-06
---

# Livepeer wire headers

This document defines the `Livepeer-*` HTTP header conventions used between gateway,
broker, and (where relevant) the gateway resolver. Every interaction-mode spec depends
on this document.

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
| `Livepeer-Spec-Version` | request → broker | yes | gateway | broker |
| `Livepeer-Mode` | request → broker | yes | gateway | broker |
| `Livepeer-Request-Id` | request → broker | optional | gateway | broker (echoed back in responses + logs) |
| `Livepeer-Backoff` | response from broker | when 503 | broker | gateway |
| `Livepeer-Work-Units` | response from broker | when applicable | broker | gateway |
| `Livepeer-Health-Status` | response on `/registry/health` | yes (on that path) | broker | gateway resolver |
| `Livepeer-Error` | response from broker on error | when error | broker | gateway |

## Header reference

### `Livepeer-Capability`

The capability identifier this request is paying for.

- **Value:** opaque UTF-8 string from the orch's manifest.
- **Example:** `Livepeer-Capability: openai:chat-completions:llama-3-70b`
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

The envelope contains:

- `ticket` — probabilistic micropayment ticket per the existing payment-daemon
  protocol (carried over from the suite).
- `capability_id` — MUST match the request's `Livepeer-Capability` header.
- `offering_id` — MUST match the request's `Livepeer-Offering` header.
- `expected_max_units` — gateway's upper-bound estimate of work units this request
  will consume; broker uses this for cap-and-bound debit.

Behavior:

- Mismatch between header and envelope → broker rejects (401 + `Livepeer-Error:
  payment_envelope_mismatch`).
- Failed ticket validation (signature, replay, insufficient face value) → 401 +
  `Livepeer-Error: payment_invalid`.
- The envelope's wire shape is owned by `payment-daemon`; the protobuf definition
  lives there. This document references it; do not duplicate.

### `Livepeer-Spec-Version`

The spec-wide SemVer the gateway is speaking.

- **Value:** `<major>.<minor>` or `<major>.<minor>.<patch>`. Receivers MUST validate
  only the major version; minor and patch are non-breaking by definition.
- **Example:** `Livepeer-Spec-Version: 1.0`
- The broker MUST reject (505 + `Livepeer-Error: spec_version_unsupported`) any
  request with a major version it does not implement.

### `Livepeer-Mode`

The interaction mode + major version the gateway is using to wrap this request.

- **Value:** `<mode-name>@v<major>` (per Q2 hybrid SemVer).
- **Example:** `Livepeer-Mode: http-stream@v1`
- The broker MUST reject (505 + `Livepeer-Error: mode_unsupported`) if it does not
  implement that mode + major version for the named capability.
- Why this is a header (not just derived from the manifest): self-describing
  requests survive intermediaries, simplify logs, and let the broker fast-fail
  before unpacking the payment envelope.

### `Livepeer-Request-Id`

Optional. UUID v4 (or any opaque short string ≤64 chars). Used for request
correlation across gateway → broker → backend.

- **Example:** `Livepeer-Request-Id: 550e8400-e29b-41d4-a716-446655440000`
- The broker SHOULD echo the value in response headers and emit it in logs and
  metrics labels.
- If absent, the broker MAY generate its own and include it in the response.

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
- Set on successful responses to one-shot modes (`http-reqresp`, `http-multipart`,
  `http-stream` upon stream end).
- For session/streaming modes (`ws-realtime`, `session-control-plus-media`,
  `rtmp-ingress-hls-egress`), reported via the mode's debit cadence — see each
  `modes/<mode>.md`.
- The gateway's payment-daemon sender uses this for reconciliation if the pre-debit
  estimate differed.

### `Livepeer-Health-Status`

Set by the broker on responses to `GET /registry/health`. Indicates the orch's live
capacity status for each currently-served capability.

- **Value:** a JSON object literal (URL-encoded if the JSON contains commas, per
  RFC 7230 header-value rules).
- **Example:**
  ```
  Livepeer-Health-Status: {"openai:chat-completions:llama-3-70b":"available","video:transcode.live.rtmp":"saturated"}
  ```
- Each value MUST be one of: `"available" | "saturated" | "draining" | "down"`.
- Gateways poll this every 15-30 seconds. The full three-layer health model lives
  in [`backend-health.md`](../../docs/design-docs/) (TBD).
- Alternative: place the JSON in the response body. Header form is preferred for
  consistency with the `Livepeer-*` family and to allow `HEAD` checks.

### `Livepeer-Error`

On any non-2xx response, the broker SHOULD set a machine-readable error code.

- **Value:** one of the codes in [Error codes](#error-codes) below.
- The response body SHOULD also include a JSON object with structured error info
  (see [Error body](#error-body)).

## Error codes

| Code | HTTP status | Meaning |
|---|---|---|
| `capability_not_served` | 404 | The orch does not currently advertise this `Livepeer-Capability`. |
| `offering_not_served` | 404 | The capability is served but the requested offering is not. |
| `payment_envelope_mismatch` | 401 | `Livepeer-Payment` envelope contents disagree with header values. |
| `payment_invalid` | 401 | Ticket failed validation (signature, replay, insufficient face value). |
| `spec_version_unsupported` | 505 | Broker does not implement the requested `Livepeer-Spec-Version`. |
| `mode_unsupported` | 505 | Broker does not implement the requested `Livepeer-Mode` for this capability. |
| `backend_unavailable` | 502 | Backend reachable but returned an error the broker can't recover from. |
| `capacity_exhausted` | 503 | Broker has no slots; see `Livepeer-Backoff`. |
| `internal_error` | 500 | Anything else. |

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
  `Livepeer-Offering`, `Livepeer-Payment`, `Livepeer-Spec-Version`,
  `Livepeer-Mode`) MUST all be present on any paid request.

## Forwarding behavior (broker → backend)

The broker is a transparent proxy with the following obligations:

- **Strip all `Livepeer-*` headers** before forwarding to the backend. The backend
  MUST NOT see Livepeer protocol headers.
- **Inject backend-specific auth** when declared in `host-config.yaml`. For
  example: `Authorization: Bearer <vault-resolved-secret>` when reselling a
  third-party API.
- **Pass through application-level headers** (`Content-Type`, `Accept`,
  `User-Agent`, etc.) at the implementer's discretion. Per-mode specs MAY further
  constrain this.
- **Echo `Livepeer-Request-Id` in logs**, even though it's stripped from the
  outbound request.

## Conformance

The conformance suite (`tztcloud/livepeer-conformance:<tag>`) verifies, at minimum:

- All required request headers parsed correctly.
- All header/envelope mismatch paths produce the right `Livepeer-Error` codes.
- 503 + `Livepeer-Backoff` round-trip behavior.
- `Livepeer-Work-Units` post-`Serve` accounting.
- `Livepeer-Spec-Version` and `Livepeer-Mode` rejection on unsupported values.
- Forwarding behavior — broker strips `Livepeer-*` and injects declared backend
  auth.

See [`../conformance/`](../conformance/).

## Changelog

| Spec version | Change |
|---|---|
| 0.1.0 | Initial draft. |
