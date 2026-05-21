---
mode_name: live-session-gateway-ingest
version: 0.1.0
status: accepted
spec_version: 0.1.0
last_updated: 2026-05-20
---

# Mode: `live-session-gateway-ingest`

Broker-owned live session authority with a gateway-owned public RTMP ingest
plane and a remote runner that writes HLS into gateway-owned object storage.

Target topology:

`client -> gateway:1935 -> broker/payment -> live runner -> gateway-owned S3/CDN`

## When to use this mode

- Public RTMP ingest must stay gateway-owned while the encode runtime stays
  orchestrator-owned.
- The gateway issues customer-facing RTMP and playback coordinates, but the
  broker remains authoritative for session open, top-ups, debits, and forced
  shutdown.

## When NOT to use this mode

- Orchestrator-owned public ingest and playback URLs ->
  `live-session-remote-runner@v0`.
- Broker-local RTMP/FFmpeg/HLS execution -> `rtmp-ingress-hls-egress@v0`.

## Capability identity

This mode uses the shared live capability id `video:transcode.live`.

In the current broker/config model, offerings are still the tuple key used to
distinguish variants. To advertise both live session modes on the same broker,
operators SHOULD publish:

- `video:transcode.live` + offering `default` + mode `live-session-remote-runner@v0`
- `video:transcode.live` + offering `gateway-ingest` + mode `live-session-gateway-ingest@v0`

## Wire shape

### Session-open

`POST /v1/cap`

Required headers:

- `Content-Type: application/json`
- `Livepeer-Capability: video:transcode.live`
- `Livepeer-Offering: <offering id>`
- `Livepeer-Mode: live-session-gateway-ingest@v0`
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
    "idle_timeout_seconds": 120
  },
  "output_credential": {
    "endpoint": "https://s3-dev.xode.app",
    "region": "us-east-1",
    "bucket": "lvp-video-ingest",
    "key_prefix": "live-out/084357a5/6d8f4a4d/",
    "access_key_id": "AKIAxxxxxxxxxxxxxxxxxx",
    "secret_access_key": "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
    "session_token": "FwoGZXIvYXdzEN...",
    "expires_at": "2026-05-20T22:10:00Z"
  },
  "ingest_accept": {
    "stream_key": "gws_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
  }
}
```

Rules:

- `output_credential` is the short-lived object-storage credential the runner
  uses to PUT manifests and media under `key_prefix`.
- `output_credential.session_token` is required and carries the gateway-issued
  session credential the runner uses when signing object-storage PUTs.
- `ingest_accept.stream_key` is the credential the gateway presents when it
  opens the private RTMP publish to the runner.

Response:

```json
{
  "broker_session_id": "bsess_01jv6f6w0rpk6n6k7e2f1v9r9a",
  "runner_session_id": "rsess_01jv6f6w3z2q8gw1dvj9mpm6zb",
  "work_id": "3f8a1dd7-4cf1-4f4b-b7b3-bbdf819e63b4",
  "state": "ready",
  "private_ingest_url": "rtmp://198.51.100.42:19350/live/gws_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
  "control": {
    "topup_url": "https://broker.example.com/v1/cap/bsess_01jv6f6w0rpk6n6k7e2f1v9r9a/topup",
    "status_url": "https://broker.example.com/v1/cap/bsess_01jv6f6w0rpk6n6k7e2f1v9r9a",
    "end_url": "https://broker.example.com/v1/cap/bsess_01jv6f6w0rpk6n6k7e2f1v9r9a/end"
  },
  "expires_at": "2026-05-20T22:10:00Z"
}
```

Rules:

- No customer-facing `rtmp_url`, `stream_key`, or `hls_url` are returned by
  this mode. Those are gateway-owned.
- `private_ingest_url` is the RTMP URL the gateway opens for its outbound
  publish to the runner.

### Session GET / topup / end

Top-up and end semantics match `live-session-remote-runner@v0`.

`GET /v1/cap/{broker_session_id}` returns the same broker/runner/work state but
omits the broker-owned `media` block because media coordinates are
gateway-owned in this mode.

## Broker-to-runner transport

`POST /v1/video/live/sessions`

```json
{
  "broker_session_id": "bsess_01jv6f6w0rpk6n6k7e2f1v9r9a",
  "work_id": "3f8a1dd7-4cf1-4f4b-b7b3-bbdf819e63b4",
  "capability_id": "video:transcode.live",
  "offering_id": "default",
  "session_params": {
    "name": "launch-stream"
  },
  "output_credential": {
    "endpoint": "https://s3-dev.xode.app",
    "region": "us-east-1",
    "bucket": "lvp-video-ingest",
    "key_prefix": "live-out/084357a5/6d8f4a4d/",
    "access_key_id": "AKIAxxxxxxxxxxxxxxxxxx",
    "secret_access_key": "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx",
    "session_token": "FwoGZXIvYXdzEN...",
    "expires_at": "2026-05-20T22:10:00Z"
  },
  "ingest_accept": {
    "stream_key": "gws_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
  },
  "broker_callbacks": {
    "event_url": "https://broker.example.com/internal/v1/live/events",
    "auth_token": "cb_01jv6f7h1y3n3ng8v9f0zw9vhe"
  }
}
```

Response includes `runner_session_id`, `state`, `private_ingest_url`, and
either `expires_at` or a timestamp the broker can use to derive it.

### Status hardening

The runner side of this mode MUST expose a stricter status and event model than
the older remote-runner contract:

- States: `provisioning`, `ready`, `publishing`, `uploading`, `stalled`,
  `ending`, `ended`, `failed`
- Status fields include ingest readiness and packet-flow timestamps plus S3 PUT
  health timestamps/counters
- Callback events distinguish `session.ready`, `session.publish_started`,
  `session.publish_stopped`, `session.upload.healthy`, `session.upload.failed`,
  `session.ended`, and `session.failed`

The broker MAY continue to accept legacy `session.started` and
`session.heartbeat` events for backwards-compatible orchestrator deployments,
but gateway-ingest implementations SHOULD use the explicit event vocabulary
above.

## Conformance

Minimum scenarios:

- open -> broker returns broker session id, runner session id,
  `private_ingest_url`, and control URLs
- invalid output credential -> runner fails with
  `close_reason: output_credential_invalid`
- ingest auth mismatch -> runner fails with
  `close_reason: ingest_authentication_failed`

Fixtures: `conformance/fixtures/live-session-gateway-ingest/*.yaml`.

## Changelog

| Mode version | Date | Change |
|---|---|---|
| 0.1.0 | 2026-05-20 | Initial gateway-ingest live-runner contract. |
