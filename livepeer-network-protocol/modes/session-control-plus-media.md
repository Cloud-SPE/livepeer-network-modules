---
mode_name: session-control-plus-media
version: 0.1.0
status: draft (proposed)
spec_version: 0.1.0
last_updated: 2026-05-06
---

# Mode: `session-control-plus-media`

HTTP session-open + WebSocket control plane + **capability-defined media plane**.
The most flexible mode — covers vtuber sessions and other workloads where the
media transport is not RTMP/HLS but something else (pytrickle, SRT, custom
chunked uploads, etc.).

Delta document. Inherits payment cadence from
[`ws-realtime@v0`](./ws-realtime.md); session-open shape from
[`rtmp-ingress-hls-egress@v0`](./rtmp-ingress-hls-egress.md).

## When to use this mode

- VTuber session (`livepeer:vtuber-session`) — control WebSocket for chat/state +
  pytrickle media plane for AV out.
- Any long-lived stateful workload where the media plane is **not** RTMP-in/HLS-out
  and **not** a single bidirectional WebSocket.
- Workloads where the gateway needs an explicit control channel separate from the
  media transport.

## When NOT to use this mode

- Bidirectional WebSocket as the only channel → use `ws-realtime`.
- RTMP-in / HLS-out → use `rtmp-ingress-hls-egress`.
- Single-shot HTTP request/response → `http-reqresp` / `http-stream` / `http-multipart`.

## Wire shape

### Session-open (request)

Identical structure to `rtmp-ingress-hls-egress` session-open: `POST /v1/cap`
with the five required Livepeer-* headers and a capability-defined JSON body.
`Livepeer-Mode: session-control-plus-media@v0`.

### Session-open (response, success)

```
HTTP/1.1 202 Accepted
Livepeer-Request-Id: 550e8400-e29b-41d4-a716-446655440000
Content-Type: application/json

{
  "session_id": "sess_xyz789",
  "control_url": "wss://broker-a.orch.example.com/v1/cap/sess_xyz789/control",
  "media": {
    "schema": "<capability-defined media descriptor>"
  },
  "expires_at": "2026-05-06T13:34:56Z"
}
```

- `control_url` — REQUIRED. The WebSocket control endpoint for this session.
- `media` — capability-defined media-plane descriptor. The protocol does not
  interpret this object; the capability defines its schema (e.g. for a vtuber
  session: `{ "publish_url": "https://...", "publish_auth": "..." }` for the
  pytrickle publisher).
- `session_id` and `expires_at` — same as `rtmp-ingress-hls-egress`.

### Control plane (REQUIRED WebSocket)

The gateway MUST open `control_url` immediately after the session-open response.
If not opened within `expires_at`, the broker auto-closes the session and
refunds.

Frame format is capability-defined JSON (recommended). The protocol expects
**control-plane semantics**:

- Broker → gateway: `session.started`, `session.balance.low`,
  `session.balance.refilled`, `session.usage.tick`, `session.error`,
  `session.ended`.
- Gateway → broker: `session.end` (graceful shutdown), capability-defined
  command messages (e.g., for vtuber: `set_persona`, `interject_text`).

The control WebSocket is **NOT** the media plane — the broker MUST NOT relay
media bytes through it. Media flows on the channel(s) described in
`media.schema`.

### Media plane

Defined entirely by the capability. The broker advertises media-plane
descriptors in the session-open response; the gateway and customer use them per
the capability's protocol. The broker's role is to **stand up** the media plane
(provision URLs, generate per-session secrets, configure the backend) — not to
carry media bytes between gateway and backend.

Examples of media-plane descriptors:

- VTuber session: `{ "publish_url": "https://trickle.example.com/...", "publish_auth": "<bearer>" }` — backend (session-runner) publishes AV to a trickle endpoint; customer (or YouTube egress worker) subscribes there.
- Custom chunked-upload workload: `{ "upload_url": "...", "auth": "..." }` — customer uploads chunks; backend processes them.
- Anything else the capability defines.

## Payment lifecycle

Same as `ws-realtime` (interim debit + reconcile):

1. Gateway estimates `expected_max_units` for the whole session.
2. Initial debit at session-open covers `runway_min_seconds`.
3. Broker debits at `cadence_seconds` (default 5).
4. Balance-low → `session.balance.low` control event + warning.
5. Balance-zero → broker emits `session.error`, closes control WS, calls
   `Reconcile` + `CloseSession`.
6. Clean session-end (gateway sends `session.end`): `Reconcile` + `CloseSession`.
7. `expires_at` without control WS open: auto-close + full refund.

### Default cadence parameters

Same defaults as `ws-realtime` (cadence_seconds=5, runway_min_seconds=15,
grace_window_ticks=2). Carries forward existing vtuber-worker-node behavior.

### Work-unit recipes

Typical extractors:

- `seconds-elapsed` — wall-clock duration of the session (simplest, sufficient
  for time-based pricing).
- `bytes-counted` — bytes flowing through media plane (when bandwidth is the
  cost driver).
- Capability-specific custom recipe declared in the offering.

## Forwarding behavior

The broker:

- Validates payment + headers on session-open POST (per `http-reqresp` rules).
- Allocates the session, starts the backend (e.g., session-runner), provisions
  media-plane URLs/secrets.
- Returns 202 with `control_url` + capability-shaped `media` descriptor.
- Hosts the control WebSocket at `control_url`; relays control messages
  bidirectionally.
- Strip-and-inject rules apply to the **session-open POST** (Livepeer-*
  stripped before any inner backend call); they do not apply to the
  capability-defined media plane (the backend manages that).

The gateway:

- Issues session-open POST.
- Receives session metadata; opens `control_url`.
- Returns the `media` descriptor to the customer (per its customer-facing
  protocol).
- Listens for control events; updates billing on `session.usage.tick`.
- Sends `session.end` to close gracefully.

## Timeouts

- **Control-WS-not-opened timeout**: if no `control_url` connection within
  `expires_at`, broker auto-closes + refunds.
- **Idle control timeout**: if no control-plane activity for N seconds (default
  `60`), broker MAY auto-close.
- **Maximum session duration**: optional cap via `extra.max_session_seconds`.

## Observability

- `livepeer_mode_session_open_total{mode="session-control-plus-media",...}`
- `livepeer_mode_session_duration_seconds{mode="session-control-plus-media",...}`
- `livepeer_mode_session_control_events_total{mode="session-control-plus-media",capability,offering,event}`
- `livepeer_mode_session_balance_low_events_total{mode="session-control-plus-media",...}`

## Versioning

Per-mode SemVer. Currently `0.1.0`.

## Conformance

Tests, at minimum:

- Session-open: returns 202 + `session_id` + `control_url` + capability-shaped
  `media` descriptor + `expires_at` after payment validation.
- Control WebSocket connectable at `control_url`; relays control messages.
- Cadence ticks emitted as `session.usage.tick` control events at
  `cadence_seconds`.
- Balance-low: `session.balance.low` control event when balance crosses
  threshold.
- Balance-zero: `session.error` event + control WS closed + `Reconcile` +
  `CloseSession`.
- Control-WS-not-opened-within-expires-at: auto-close + full refund.
- Header validation on session-open POST: same matrix as `http-reqresp`.
- Media plane is NOT carried over the control WS (verified by counting bytes
  on each surface — control plane should be small; media plane should not
  appear there).

Fixtures: `conformance/fixtures/session-control-plus-media/*.yaml`.

## Changelog

| Mode version | Date | Change |
|---|---|---|
| 0.1.0 | 2026-05-06 | Initial draft. |
