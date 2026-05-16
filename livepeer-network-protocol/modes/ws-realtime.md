---
mode_name: ws-realtime
version: 0.1.0
status: accepted
spec_version: 0.1.0
last_updated: 2026-05-06
---

# Mode: `ws-realtime`

Bidirectional WebSocket. The first non-HTTP-shaped mode; introduces interim-debit
payment cadence because the connection is long-lived and arbitrarily-sized.

This is a delta document. Where it does not say otherwise, behavior matches
[`http-reqresp@v0`](./http-reqresp.md) (header validation, forwarding rules,
backend-auth injection, error codes).

## When to use this mode

- OpenAI Realtime API (`/v1/realtime`) — bidirectional audio + tool-calling.
- VTuber `/control` channel (when offered as a standalone capability).
- Any long-lived bidirectional message channel where both sides send frames at
  arbitrary times.

## When NOT to use this mode

- Single request/response → use `http-reqresp` or `http-multipart`.
- Streaming response only (no inbound traffic after request) → use `http-stream`.
- Long-lived session with a separate media plane (RTMP, trickle, SRT, etc.) →
  use `session-control-plus-media`.
- RTMP ingest + HLS egress specifically → use `rtmp-ingress-hls-egress`.

## Wire shape

### Path

`GET /v1/cap` with WebSocket upgrade headers. (HTTP method differs from the
`POST`-based modes; the broker dispatches by `(method, Livepeer-Mode)`.)

### Opening handshake (request)

```
GET /v1/cap HTTP/1.1
Host: broker-a.orch.example.com
Connection: Upgrade
Upgrade: websocket
Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==
Sec-WebSocket-Version: 13
Livepeer-Capability: openai:realtime
Livepeer-Offering: openai-resale
Livepeer-Payment: <base64-encoded payment envelope>
Livepeer-Spec-Version: 0.1
Livepeer-Mode: ws-realtime@v0
Livepeer-Request-Id: 550e8400-e29b-41d4-a716-446655440000
```

- All five required Livepeer-* headers travel on the upgrade request — payment
  is validated **before** the upgrade completes.
- Broker rejects with appropriate HTTP status + `Livepeer-Error` if validation
  fails (no upgrade).

### Opening handshake (response, success)

```
HTTP/1.1 101 Switching Protocols
Upgrade: websocket
Connection: Upgrade
Sec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=
Livepeer-Request-Id: 550e8400-e29b-41d4-a716-446655440000
```

- After `101 Switching Protocols`, both sides exchange frames per RFC 6455.
- Frame payloads are opaque to the protocol — the capability defines them.

### Frame forwarding

- Broker MUST relay text and binary frames between gateway and backend
  bidirectionally.
- Broker MUST strip the five required `Livepeer-*` headers from the upgrade
  request before forwarding to the backend (the backend speaks plain
  WebSocket; it doesn't see Livepeer protocol).
- Broker MUST inject backend-specific auth on the upgrade-to-backend (e.g., the
  orch's OpenAI API key when reselling).
- Ping/pong control frames are handled by the broker per RFC 6455; not
  forwarded to the backend.
- Close frames (opcode 0x8): forwarded both directions; broker initiates
  shutdown (debit reconcile + CloseSession) on either close.

## Payment lifecycle

**Interim-debit + final reconcile** — the key difference from HTTP modes.

1. Gateway estimates `expected_max_units` (an upper bound for the *whole session*,
   not a per-request bound).
2. Gateway includes it in `Livepeer-Payment` envelope on the upgrade request.
3. Broker validates ticket; opens session; debits a configurable initial slice
   (default: enough for `runway_min_seconds` worth of cadence ticks at the
   maximum advertised price).
4. WebSocket upgrades; frame relay begins.
5. **Every `cadence_seconds` seconds** (default `5`), the broker:
   - Computes units consumed since the last tick via the offering's declared
     extractor (typically `seconds-elapsed` or `bytes-counted`).
   - Calls `payment-daemon.Debit(units, debitSeq)` with a monotonic `debitSeq`.
   - If the running balance falls below `runway_min_units`, broker emits a
     `Livepeer-Balance-Low` application-level message (capability-shaped
     warning, body format defined per capability) and continues.
   - If balance hits zero, broker initiates close with
     `Livepeer-Error: payment_invalid` (insufficient balance for next tick).
6. On close (either side initiates):
   - Broker computes any final partial-tick units.
   - Calls `payment-daemon.Reconcile(<total>)`.
   - Calls `payment-daemon.CloseSession()`.

The **gateway's payment-daemon-sender knows the running debit total** (via
session ledger). No mid-session "report back" is needed; the running tick total
IS the bill.

### Default cadence parameters

| Parameter | Default | Override via |
|---|---|---|
| `cadence_seconds` | 5 | offering's `extra.debit_cadence_seconds` |
| `runway_min_seconds` | 15 | offering's `extra.runway_min_seconds` |
| `grace_window_ticks` | 2 | offering's `extra.grace_window_ticks` |

These defaults match the existing vtuber-session implementation pattern.

## Forwarding behavior

In addition to the `http-reqresp` rules, for the upgrade phase:

- Broker validates payment + headers BEFORE returning `101 Switching Protocols`;
  any error short-circuits with an HTTP response, no upgrade.
- Broker strips Livepeer-* headers from the outbound upgrade request to the
  backend.
- Broker injects backend-specific auth on the outbound upgrade.

For frame relay:

- Broker MUST NOT modify frame payloads.
- Broker MAY observe frame contents to compute units (per the extractor),
  without altering them.

## Timeouts

- **Idle timeout**: if no frame is received from either side for N seconds
  (recommended default `60`), the broker initiates close. Configurable via
  `extra.idle_timeout_seconds`.
- **Maximum session duration**: optional cap (e.g. 4 hours) advertised via
  `extra.max_session_seconds`.

## Body / frame size

- Mode does not impose hard limits. Recommend max frame size of 16 MiB (RFC 6455
  allows arbitrary).
- Implementations document via `extra.max_frame_bytes`.

## Idempotency

Not applicable for stateful sessions.

## Observability

- `livepeer_mode_session_open_total{mode="ws-realtime",capability,offering,outcome}` — counter.
- `livepeer_mode_session_duration_seconds{mode="ws-realtime",capability,offering}` — histogram.
- `livepeer_mode_session_debit_ticks_total{mode="ws-realtime",capability,offering}` — counter.
- `livepeer_mode_session_balance_low_events_total{mode="ws-realtime",capability,offering}` — counter.

## Versioning

Per-mode SemVer. Currently `0.1.0`.

## Conformance

Tests, at minimum:

- Pre-upgrade payment validation: missing/invalid Livepeer-* headers produce HTTP
  error responses (no upgrade) with the right `Livepeer-Error` codes.
- Successful upgrade: `101 Switching Protocols`; frame relay both directions;
  Livepeer-* headers stripped from the outbound upgrade.
- Cadence ticks: broker calls `Debit` every `cadence_seconds` with a monotonic
  `debitSeq`; values match the extractor recipe.
- Balance-low signaling: when running balance crosses below `runway_min_units`,
  broker emits the Balance-Low application message.
- Balance-zero close: broker initiates close with `payment_invalid` when balance
  hits zero.
- Both-sides close: clean shutdown from either gateway-initiated or
  backend-initiated close.
- Ping/pong handling: broker auto-replies; not forwarded.

Fixtures: `conformance/fixtures/ws-realtime/*.yaml`.

## Changelog

| Mode version | Date | Change |
|---|---|---|
| 0.1.0 | 2026-05-06 | Initial draft. |
