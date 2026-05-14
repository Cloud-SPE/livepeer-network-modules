---
mode_name: session-control-external-media
version: 0.1.0
status: accepted
spec_version: 0.1.0
last_updated: 2026-05-14
---

# Mode: `session-control-external-media`

HTTP session-open + WebSocket control plane + **broker-fronted reverse
proxy to a long-lived, multi-session backend that owns its own media plane**.

Delta document. Inherits payment cadence and control-WS lifecycle frames from
[`session-control-plus-media@v0`](./session-control-plus-media.md);
removes broker-managed media plumbing.

## When to use this mode

- Real-time generative AI workloads where the backend is a long-lived
  multi-session process whose start-up cost (model load) precludes
  per-session container spawn. Example: Daydream Scope
  (`daydreamlive/scope:main`).
- Backends that own their own WebRTC plane (incl. ICE candidates signed
  against the backend's host) such that proxying media through the broker
  would require modifying the backend.
- Workloads where TURN/STUN is provided externally (Cloudflare TURN,
  Twilio) and the broker has no role in media relay.

## When NOT to use this mode

- Per-session backend container is desired (broker spawns, broker owns
  media relay) → use `session-control-plus-media@v0`.
- Bidirectional WebSocket is the only channel → `ws-realtime@v0`.
- RTMP-in / HLS-out → `rtmp-ingress-hls-egress@v0`.
- Single-shot HTTP request/response → `http-reqresp` /
  `http-stream` / `http-multipart`.

## Wire shape

### Session-open (request)

Identical structure to `session-control-plus-media@v0` session-open:
`POST /v1/cap` with the five required Livepeer-* headers and a
capability-defined JSON body. `Livepeer-Mode: session-control-external-media@v0`.

### Session-open (response, success)

```
HTTP/1.1 202 Accepted
Livepeer-Request-Id: 550e8400-e29b-41d4-a716-446655440000
Content-Type: application/json

{
  "session_id": "sess_xyz789",
  "control_url": "wss://broker-a.orch.example.com/v1/cap/sess_xyz789/control",
  "media": {
    "schema": "scope-passthrough/v0",
    "scope_url": "https://broker-a.orch.example.com/_scope/sess_xyz789/"
  },
  "expires_at": "2026-05-06T13:34:56Z"
}
```

- `control_url` — REQUIRED. The WebSocket control endpoint for this session.
- `media.schema` — REQUIRED. Identifies the backend-API shape the gateway
  and customer can expect at `media.scope_url`. Capability-defined.
- `media.scope_url` — REQUIRED. The broker-fronted reverse-proxy URL the
  gateway forwards customer traffic to. See [Reverse-proxy plane](#reverse-proxy-plane) below.
- `session_id` and `expires_at` — same as `session-control-plus-media@v0`.

### Control plane (REQUIRED WebSocket)

The gateway MUST open `control_url` immediately after the session-open response.
If not opened within `expires_at`, the broker auto-closes the session and
refunds.

Frame format is JSON envelope (same as `session-control-plus-media@v0`).
The frame vocabulary is **lifecycle-only** — capability-defined runtime
commands MUST NOT flow on this WebSocket. They flow on the backend's own
control channel (e.g. WebRTC data channel, backend-defined WebSocket)
which is reached through the reverse-proxy plane.

- Broker → gateway: `session.started`, `session.balance.low`,
  `session.balance.refilled`, `session.usage.tick`, `session.error`,
  `session.ended`.
- Gateway → broker:
  - `session.end` — graceful shutdown.
  - `session.topup` — mid-session payment refill (same envelope as
    `session-control-plus-media@v0`).

The control WebSocket is **NOT** the media plane and **NOT** a backend
control channel. Capability-defined runtime control (prompt updates,
parameter changes, etc.) is out of band.

### Reverse-proxy plane

`media.scope_url` is a broker-owned HTTPS reverse proxy mounted at
`/_scope/<session_id>/` on the broker's listener. It forwards HTTP requests
from the gateway (and, transitively, the customer) to the workload backend's
HTTP API.

The proxy:

- Forwards every path under `/_scope/<session_id>/` to the backend
  configured by the capability's `backend.url`, stripping the
  `/_scope/<session_id>` prefix.
- Strips inbound `Livepeer-*` headers before forwarding (consistent with
  `http-reqresp`-family rules).
- Authorises every request against the live session record — requests for
  unknown or closed `session_id` get 404.
- **Short-circuits backend session-lifecycle calls.** Specifically, the
  capability declares which backend paths begin/end a backend-side session.
  When the proxy sees them, it does not forward verbatim; instead it:
  - Reports `session.started` (the first time backend reports a session
    has begun) on the control WS.
  - Starts the seconds-elapsed clock at first-contact with the backend's
    session-start endpoint.
  - On backend session-stop, treats it as a graceful termination from the
    backend's side and emits `session.ended`.
- Does NOT touch media bytes. Once the backend's HTTP API completes a
  WebRTC SDP/ICE exchange, media flows browser ↔ TURN ↔ backend directly,
  out of the broker entirely.

### Media plane

Defined entirely by the workload. The broker has no role in carrying media
bytes. The media plane is established by the customer using descriptors
returned through the reverse-proxy plane (e.g. SDP answers and ICE
candidates returned from the backend's `/api/v1/webrtc/offer`-equivalent).

Examples of `media.schema` values:

- `scope-passthrough/v0` — Daydream Scope's native HTTP API at
  `scope_url`. SDP/ICE exchange via `POST /api/v1/webrtc/offer`. Media
  flows browser ↔ Cloudflare TURN ↔ Scope.

The broker MUST NOT relay media bytes. The conformance suite verifies
this by counting bytes on the control WS and the reverse-proxy plane
under load.

## Payment lifecycle

Same shape as `session-control-plus-media@v0` (interim debit + reconcile):

1. Gateway estimates `expected_max_units` for the whole session.
2. Initial debit at session-open covers `runway_min_seconds`.
3. Broker debits at `cadence_seconds` (default 5).
4. Balance-low → `session.balance.low` control event.
5. Balance-zero → broker emits `session.error`, closes control WS, closes
   backend-side session via the capability-declared stop endpoint,
   calls `Reconcile` + `CloseSession`.
6. Clean session-end (gateway sends `session.end`): broker closes
   backend-side session, then `Reconcile` + `CloseSession`.
7. `expires_at` without control WS open: auto-close + full refund.
8. Backend disconnect (proxy detects 5xx storm or the backend declares
   the session terminated): broker emits `session.error` with reason
   `runner_disconnect`, closes control WS, refunds remainder.

### Default cadence parameters

Same defaults as `session-control-plus-media@v0`: `cadence_seconds=5`,
`runway_min_seconds=15`, `grace_window_ticks=2`.

### Work-unit recipes

The canonical extractor is **`seconds-elapsed`** with granularity 1.
Wall-clock starts at first-contact with the backend's session-start
endpoint through the reverse proxy (not at session-open ack — the gateway
has not started using the GPU yet).

Other extractors (e.g. `bytes-counted`) MAY be declared in the offering
where the capability has a different cost driver.

## Forwarding behavior

The broker:

- Validates payment + headers on session-open POST (per `http-reqresp` rules).
- Allocates the session record, mints `session_id`, derives `scope_url`.
- Returns 202 with `control_url`, `scope_url`, `expires_at`.
- Hosts the control WebSocket at `control_url`.
- Hosts the reverse proxy at `scope_url`.
- Authorises every reverse-proxy request against the session record.
- Strips Livepeer-* headers from reverse-proxy forwards.

The gateway:

- Issues session-open POST.
- Receives `session_id`, `control_url`, `scope_url`.
- Opens `control_url`; subscribes to lifecycle events.
- Returns `scope_url` to the customer (as the customer-facing backend
  URL).
- Listens for control events; updates its session state on
  `session.usage.tick`.
- Sends `session.end` to close gracefully.

## Timeouts

- **Control-WS-not-opened timeout**: if no `control_url` connection
  within `expires_at`, broker auto-closes + refunds.
- **Idle control timeout**: if no control-plane activity for N seconds
  (default `60`), broker MAY auto-close.
- **Backend-unresponsive timeout**: if the reverse-proxy detects the
  backend has been unreachable for N seconds (default `30`), the broker
  emits `session.error` with reason `runner_disconnect`, closes the
  control WS, and refunds the remainder.
- **Maximum session duration**: optional cap via
  `extra.max_session_seconds`.

## Observability

- `livepeer_mode_session_open_total{mode="session-control-external-media",...}`
- `livepeer_mode_session_duration_seconds{mode="session-control-external-media",...}`
- `livepeer_mode_session_control_events_total{mode="session-control-external-media",capability,offering,event}`
- `livepeer_mode_session_balance_low_events_total{mode="session-control-external-media",...}`
- `livepeer_mode_proxy_requests_total{mode="session-control-external-media",capability,offering,result}`
- `livepeer_mode_proxy_bytes_total{mode="session-control-external-media",capability,offering,direction}`

## Versioning

Per-mode SemVer. Currently `0.1.0`.

## Conformance

Tests, at minimum:

- Session-open: returns 202 + `session_id` + `control_url` + `media.scope_url`
  + `expires_at` after payment validation.
- Control WebSocket connectable at `control_url`; relays lifecycle events
  only (rejects capability-defined command frames).
- Reverse proxy at `scope_url` forwards a non-lifecycle backend call
  (e.g. `GET /api/v1/pipeline/status`) verbatim aside from
  Livepeer-* stripping.
- Reverse proxy rejects requests for unknown / closed `session_id` with 404.
- Cadence ticks emitted as `session.usage.tick` at `cadence_seconds`.
- Balance-low: `session.balance.low` control event when balance crosses
  threshold.
- Balance-zero: `session.error` event + control WS closed + reverse-proxy
  short-circuits backend stop endpoint + `Reconcile` + `CloseSession`.
- Control-WS-not-opened-within-expires-at: auto-close + full refund.
- Header validation on session-open POST: same matrix as `http-reqresp`.
- The control WS carries no application-defined command frames (verified
  by counting frame types on the control surface; only lifecycle types
  permitted).
- The reverse-proxy plane does not carry media bytes (verified by byte
  counts on the proxy vs. observed backend WebRTC traffic).

Fixtures: `conformance/fixtures/session-control-external-media/*.yaml`.

## Changelog

| Mode version | Date | Change |
|---|---|---|
| 0.1.0 | 2026-05-14 | Initial draft. |
