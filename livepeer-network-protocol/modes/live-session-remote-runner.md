---
mode_name: live-session-remote-runner
version: 0.1.0
status: accepted
spec_version: 0.1.0
last_updated: 2026-05-20
---

# Mode: `live-session-remote-runner`

Broker-owned live session authority with a **remote runner-owned RTMP/HLS media
runtime**.

The gateway talks to the broker for paid session control. The broker talks to a
remote live runner for media-runtime allocation, usage events, and forced
shutdown. The runner never talks to `payment-daemon` directly.

Target topology:

`client -> transcode gateway -> broker/payment -> live runner`

## When to use this mode

- Live RTMP ingest -> FFmpeg transcode -> HLS playback where the media runtime
  is not broker-local.
- Offerings where the broker must remain authoritative for session open,
  top-ups, debit/runway enforcement, and forced shutdown.
- Long-lived live sessions with runner-reported usage/state over HTTP events.

## When NOT to use this mode

- Broker-local RTMP/FFmpeg/HLS execution -> `rtmp-ingress-hls-egress@v0`.
- Broker-managed per-session interactive media relay -> `session-control-plus-media@v0`.
- Generic HTTP reverse-proxy passthrough to a workload-owned API -> use a
  different mode; this mode is not a generic passthrough contract.

## Wire shape

### Session-open (request)

`POST /v1/cap`

Required headers:

- `Content-Type: application/json`
- `Livepeer-Capability: <live capability id>`
- `Livepeer-Offering: <offering id>`
- `Livepeer-Mode: live-session-remote-runner@v0`
- `Livepeer-Spec-Version: 0.1`
- `Livepeer-Request-Id: <uuid>`
- `Livepeer-Payment: <base64 envelope>`

Body:

```json
{
  "gateway_session_id": "6d8f4a4d-09d7-4c1d-8d3e-c7d60c6114c4",
  "session_params": {
    "name": "launch-stream",
    "ladder": {
      "rungs": [
        { "name": "source", "passthrough": true },
        { "name": "720p", "width": 1280, "height": 720, "bitrate_kbps": 2500 },
        { "name": "480p", "width": 854, "height": 480, "bitrate_kbps": 1000 },
        { "name": "240p", "width": 426, "height": 240, "bitrate_kbps": 400 }
      ]
    },
    "idle_timeout_seconds": 30
  }
}
```

### Session-open (response, success)

```json
{
  "gateway_session_id": "6d8f4a4d-09d7-4c1d-8d3e-c7d60c6114c4",
  "broker_session_id": "bsess_01jv6f6w0rpk6n6k7e2f1v9r9a",
  "runner_session_id": "rsess_01jv6f6w3z2q8gw1dvj9mpm6zb",
  "work_id": "3f8a1dd7-4cf1-4f4b-b7b3-bbdf819e63b4",
  "state": "ready",
  "media": {
    "ingest": {
      "rtmp_url": "rtmp://ingest.example.com/live",
      "stream_key": "lvk_4mY6fB7T2qR8kP1sW3dX9nL5cH0zA"
    },
    "playback": {
      "hls_url": "https://playback.example.com/live/rsess_01jv6f6w3z2q8gw1dvj9mpm6zb/master.m3u8"
    }
  },
  "control": {
    "topup_url": "https://broker.example.com/v1/cap/bsess_01jv6f6w0rpk6n6k7e2f1v9r9a/topup",
    "status_url": "https://broker.example.com/v1/cap/bsess_01jv6f6w0rpk6n6k7e2f1v9r9a",
    "end_url": "https://broker.example.com/v1/cap/bsess_01jv6f6w0rpk6n6k7e2f1v9r9a/end"
  },
  "expires_at": "2026-05-20T20:10:00Z"
}
```

Rules:

- Payment is required.
- Broker allocates `broker_session_id`.
- Broker allocates `work_id`.
- Broker MUST bind to a remote runner session before returning success.
- Runner media coordinates are returned in the session-open response.
- Plaintext `stream_key` is returned once here only.

### Session top-up

`POST /v1/cap/{broker_session_id}/topup`

Required headers:

- `Content-Type: application/json`
- `Livepeer-Request-Id: <uuid>`
- `Livepeer-Payment: <base64 envelope>`

Body:

```json
{
  "gateway_session_id": "6d8f4a4d-09d7-4c1d-8d3e-c7d60c6114c4"
}
```

Response:

```json
{
  "broker_session_id": "bsess_01jv6f6w0rpk6n6k7e2f1v9r9a",
  "work_id": "3f8a1dd7-4cf1-4f4b-b7b3-bbdf819e63b4",
  "state": "publishing",
  "balance": {
    "status": "ok",
    "runway_seconds_estimate": 184
  }
}
```

Rules:

- Payment is required.
- Top-up MUST credit the existing receiver-side payment session.
- Top-up MUST NOT create a new logical live session.

### Session status

`GET /v1/cap/{broker_session_id}`

Response:

```json
{
  "gateway_session_id": "6d8f4a4d-09d7-4c1d-8d3e-c7d60c6114c4",
  "broker_session_id": "bsess_01jv6f6w0rpk6n6k7e2f1v9r9a",
  "runner_session_id": "rsess_01jv6f6w3z2q8gw1dvj9mpm6zb",
  "work_id": "3f8a1dd7-4cf1-4f4b-b7b3-bbdf819e63b4",
  "state": "publishing",
  "media": {
    "ingest": {
      "rtmp_url": "rtmp://ingest.example.com/live"
    },
    "playback": {
      "hls_url": "https://playback.example.com/live/rsess_01jv6f6w3z2q8gw1dvj9mpm6zb/master.m3u8"
    }
  },
  "started_at": "2026-05-20T19:12:09Z",
  "last_heartbeat_at": "2026-05-20T19:13:01Z",
  "ended_at": null,
  "close_reason": null
}
```

Rules:

- No plaintext stream key here.
- Broker SHOULD serve this from persisted broker session state without a
  synchronous runner fetch on every call.

### Session end

`POST /v1/cap/{broker_session_id}/end`

Body:

```json
{
  "reason": "gateway_close"
}
```

Response:

```json
{
  "broker_session_id": "bsess_01jv6f6w0rpk6n6k7e2f1v9r9a",
  "runner_session_id": "rsess_01jv6f6w3z2q8gw1dvj9mpm6zb",
  "state": "ended",
  "close_reason": "gateway_close",
  "ended_at": "2026-05-20T19:14:23Z"
}
```

Rules:

- Idempotent.
- Broker MUST terminate the runner session.
- Broker MUST close payment state.

## Broker-to-runner transport

Runner transport is HTTP. Broker authenticates to runner with bearer auth or an
equivalent operator-configured mechanism.

### Create runner session

`POST /v1/video/live/sessions`

```json
{
  "broker_session_id": "bsess_01jv6f6w0rpk6n6k7e2f1v9r9a",
  "work_id": "3f8a1dd7-4cf1-4f4b-b7b3-bbdf819e63b4",
  "capability_id": "livepeer:transcode/live-rtmp-hls-abr",
  "offering_id": "default",
  "session_params": {
    "name": "launch-stream"
  },
  "broker_callbacks": {
    "event_url": "https://broker.example.com/internal/v1/live/events",
    "auth_token": "cb_01jv6f7h1y3n3ng8v9f0zw9vhe"
  }
}
```

Response includes `runner_session_id`, `state`, ingest coordinates, playback
coordinates, and `created_at`.

### Query runner session

`GET /v1/video/live/sessions/{runner_session_id}`

Response includes `runner_session_id`, `broker_session_id`, `state`,
`started_at`, `last_packet_at`, `last_heartbeat_at`, and `close_reason`.

### Terminate runner session

`DELETE /v1/video/live/sessions/{runner_session_id}`

```json
{
  "reason": "insufficient_balance"
}
```

Rules:

- Idempotent.
- Broker MUST use this path for forced shutdown on insufficient balance.

## Runner events into broker

`POST /internal/v1/live/events`

Required headers:

- `Content-Type: application/json`
- `Authorization: Bearer <callback token>`

Payload:

```json
{
  "broker_session_id": "bsess_01jv6f6w0rpk6n6k7e2f1v9r9a",
  "runner_session_id": "rsess_01jv6f6w3z2q8gw1dvj9mpm6zb",
  "event_id": "evt_01jv6f9q2tk7yhm6m63y8j5b6v",
  "sequence": 17,
  "event_type": "session.usage.tick",
  "event_time": "2026-05-20T19:13:00Z",
  "state": "publishing",
  "usage": {
    "unit": "output_seconds",
    "delta": 5,
    "total": 60
  },
  "close_reason": null,
  "details": {}
}
```

Required event types:

- `session.started`
- `session.heartbeat`
- `session.usage.tick`
- `session.failed`
- `session.ended`

Rules:

- `sequence` MUST be monotonic per runner session.
- `event_id` MUST be unique for idempotency.
- `usage.total` is authoritative cumulative usage.
- Broker SHOULD derive debit deltas from cumulative usage, not trust
  `usage.delta` blindly.

## Payment lifecycle

Broker remains the only component mutating receiver-side balance.

Required semantics:

1. Validate initial payment on open.
2. Open receiver-side payment session.
3. Accept top-ups for the same `work_id`.
4. Convert runner usage events into `DebitBalance` calls.
5. Run `SufficientBalance` checks on cadence or usage events.
6. If insufficient:
   - terminate runner session
   - mark broker session ended/failed with `insufficient_balance`
   - close payment session cleanly

The runner MUST NEVER call `payment-daemon` directly.

## Config and routing expectations

Offerings using this mode MUST support a remote live-runner backend transport.
Resolver/coordinator/pool surfaces publish/select these offerings as ordinary
 capabilities; no broker-local FFmpeg transport is implied by the mode name.

## Observability

- `livepeer_mode_session_open_total{mode="live-session-remote-runner",...}`
- `livepeer_mode_session_duration_seconds{mode="live-session-remote-runner",...}`
- `livepeer_mode_runner_events_total{mode="live-session-remote-runner",event_type,...}`
- `livepeer_mode_session_balance_low_events_total{mode="live-session-remote-runner",...}`
- `livepeer_mode_runner_forced_shutdown_total{mode="live-session-remote-runner",reason}`

## Conformance

Minimum scenarios:

- open -> broker returns broker session id, runner session id, ingest/playback
  coordinates, and control URLs
- publish -> runner event intake updates authoritative session state
- usage tick -> broker debits from cumulative usage
- top-up -> same logical session/work id receives additional funded runway
- close -> broker ends runner session and closes payment state
- insufficient balance -> broker forces runner shutdown and marks session with
  `insufficient_balance`
- runner failure -> broker marks failed state and closes payment state

Fixtures: `conformance/fixtures/live-session-remote-runner/*.yaml`.

## Changelog

| Mode version | Date | Change |
|---|---|---|
| 0.1.0 | 2026-05-20 | Initial remote live-runner contract. Replaces the older external-media reverse-proxy shape. |
