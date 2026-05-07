---
mode_name: rtmp-ingress-hls-egress
version: 0.1.0
status: accepted
spec_version: 0.1.0
last_updated: 2026-05-06
---

# Mode: `rtmp-ingress-hls-egress`

RTMP ingress → HLS playlist + segments egress. Live video transcode pattern. The
session is set up via an HTTP `POST /v1/cap` (which returns the RTMP push URL and
HLS playback URL); the **media flows over RTMP and HTTP**, not over a single
HTTP request.

Delta document. Inherits header semantics from
[`http-reqresp@v0`](./http-reqresp.md); inherits payment cadence from
[`ws-realtime@v0`](./ws-realtime.md).

## When to use this mode

- Live video transcode (broadcast input → ABR HLS output).
- Any capability where customer-facing protocol is RTMP push + HLS pull.

## When NOT to use this mode

- VOD transcode (single file in, files out) → use `http-reqresp`.
- Bidirectional realtime AV → use `ws-realtime`.
- Custom session protocol with a non-RTMP/non-HLS media plane → use
  `session-control-plus-media`.

## Wire shape

### Session-open (request)

```
POST /v1/cap HTTP/1.1
Host: broker-a.orch.example.com
Livepeer-Capability: video:transcode.live.rtmp
Livepeer-Offering: h264-1080p30
Livepeer-Payment: <base64-encoded payment envelope>
Livepeer-Spec-Version: 0.1
Livepeer-Mode: rtmp-ingress-hls-egress@v0
Livepeer-Request-Id: 550e8400-e29b-41d4-a716-446655440000
Content-Type: application/json

{
  "session_params": {
    "abr_ladder": [...],
    "preset": "h264-streaming",
    "expected_duration_seconds": 3600
  }
}
```

The session-open body is capability-defined; opaque to the protocol.

### Session-open (response, success)

```
HTTP/1.1 202 Accepted
Livepeer-Request-Id: 550e8400-e29b-41d4-a716-446655440000
Content-Type: application/json

{
  "session_id": "sess_abc123",
  "stream_key": "PtKqv9rT6Y4nB5wXyZmA1c8FdGhJlNoP",
  "rtmp_ingest_url": "rtmp://broker-a.orch.example.com:1935/sess_abc123/PtKqv9rT6Y4nB5wXyZmA1c8FdGhJlNoP",
  "hls_playback_url": "https://broker-a.orch.example.com/_hls/sess_abc123/playlist.m3u8",
  "control_url": "wss://broker-a.orch.example.com/v1/cap/sess_abc123/control",
  "expires_at": "2026-05-06T13:34:56Z"
}
```

- Broker assigns a `session_id`, generates a `stream_key`, and returns:
  - `session_id` — opaque identifier the broker uses to look up session state.
  - `stream_key` — 32-byte URL-safe random bearer token. Surfaced at the top
    of the response body so the gateway adapter can read it without URL
    parsing. Treat as a secret; never log raw.
  - `rtmp_ingest_url` — where to push RTMP. Path-based shape
    `rtmp://host:1935/<session_id>/<stream_key>` (mirrors mux / twitch /
    youtube). Query-string variants (`?key=<...>`) are rejected.
  - `hls_playback_url` — where the broker serves the LL-HLS playlist + fmp4
    segments. The URL path is itself a per-session unguessable bearer secret.
  - `control_url` — optional WebSocket endpoint for control-plane events
    (`session.balance.low`, `session.error`, `session.ended`).
  - `expires_at` — wall-clock deadline; if no RTMP push by then, session is
    auto-closed and refunded.
- 202 (not 200) because the session is queued; the actual media plane comes up
  on the first RTMP push.
- Initial debit happens at session-open (per `ws-realtime` lifecycle).

### Media plane

- **Ingest**: customer (or gateway) pushes RTMP to `rtmp_ingest_url`. The URL
  carries `<session_id>/<stream_key>` in its path; the broker parses RTMP's
  `PublishingName` on `OnPublish`, splits, and constant-time compares
  `stream_key` against its open-session record. Mismatch yields RTMP
  `_error`. Customer-facing auth (API keys, mTLS, AuthWebhookURL-style
  integration) lives gateway-side; the broker's check is defense-in-depth.
- **Egress**: broker writes the LL-HLS playlist + fmp4 segments + parts under
  `hls_playback_url`. The default LL-HLS layout is fmp4 segments + `.m4s`
  parts at 333ms part duration with a 4-segment rolling window
  (`#EXT-X-VERSION:6`); operators flip to legacy mpegts HLS v3 with
  6s segments via `--hls-legacy=true`.

### Control plane (optional WebSocket)

If the gateway opens `control_url`:

- Broker pushes control events: `session.started`, `session.balance.low`,
  `session.balance.refilled`, `session.error`, `session.ended`.
- Gateway can push commands: `session.end` (graceful shutdown).
- Frame format is capability-defined JSON (recommended, not mandatory).

If `control_url` is not opened, the session still functions; balance-low /
error signaling falls back to RTMP disconnect.

### Session-end

Either:
- Customer disconnects RTMP → broker detects no-data, drains, closes session.
- Gateway POSTs to `https://broker.example.com/v1/cap/{session_id}/end`
  (capability-defined endpoint) → broker drains, closes session.
- Balance hits zero → broker disconnects RTMP, closes session.
- `expires_at` reached without a push → broker times out, refunds, closes.

## Payment lifecycle

Same shape as `ws-realtime`:

1. Gateway estimates `expected_max_units` for the **whole session** (e.g.,
   1 hour × frames-per-second × frame-megapixels).
2. Initial debit at session-open covers `runway_min_seconds`.
3. Broker debits at `cadence_seconds` (default 5) using `ffmpeg-progress` or
   `seconds-elapsed` extractor.
4. Balance-low → control event + warning.
5. Balance-zero → broker disconnects RTMP, calls `Reconcile` + `CloseSession`.
6. On clean session-end: `Reconcile` with final accounting + `CloseSession`.

### Default cadence parameters

Same defaults as `ws-realtime`. Override via offering's `extra.*` fields.

### Work-unit recipes

Typical extractors:

- `ffmpeg-progress` — broker parses FFmpeg's `progress=...` output and emits
  per-tick frame counts (rendered as `video-frame-megapixel`).
- `seconds-elapsed` — wall-clock seconds since session-open (simpler; sufficient
  for time-based pricing).

## Forwarding behavior

The broker:

- Runs an RTMP listener on a public TCP port (typically `:1935`; configurable).
- Accepts the RTMP push, demuxes audio + video, runs FFmpeg (or equivalent) per
  the offering's declared preset, mux'd output written to the HLS sink.
- The "backend" (in `host-config.yaml`) for this mode is the FFmpeg subprocess
  itself; the broker shells out, no separate HTTP backend.
- Strip-and-inject rules don't apply to the media plane (no Livepeer-*
  headers on RTMP).

The gateway:

- Issues session-open POST with payment envelope.
- Receives `rtmp_ingest_url` + `hls_playback_url`.
- Returns those URLs to the customer (the customer's encoder pushes RTMP; the
  customer's player pulls HLS).
- Optionally opens the `control_url` WebSocket to receive session events.
- Accumulates customer billing per `Debit` events from `payment-daemon-sender`.

## Timeouts

- **No-push timeout**: if no RTMP push received within `expires_at` of session
  open, broker auto-closes and refunds.
- **Mid-stream stall**: if RTMP stream stalls for N seconds (recommended `15`),
  broker disconnects, drains HLS, closes session.

## Observability

- `livepeer_mode_session_open_total{mode="rtmp-ingress-hls-egress",...}`
- `livepeer_mode_session_duration_seconds{mode="rtmp-ingress-hls-egress",...}`
- `livepeer_mode_rtmp_bytes_in_total{mode="rtmp-ingress-hls-egress",capability,offering}`
- `livepeer_mode_hls_segments_written_total{mode="rtmp-ingress-hls-egress",capability,offering}`
- `livepeer_mode_session_balance_low_events_total{mode="rtmp-ingress-hls-egress",...}`

## Versioning

Per-mode SemVer. Currently `0.1.0`.

## Conformance

Tests, at minimum:

- Session-open: returns 202 + valid `rtmp_ingest_url` + `hls_playback_url` +
  `session_id` + `stream_key` + `expires_at` after payment validation.
- RTMP push connects to advertised URL; first HLS segment appears within N
  seconds.
- Cadence ticks happen at `cadence_seconds`; debit values match
  `ffmpeg-progress` or `seconds-elapsed` recipe.
- Balance-low control event is emitted when running balance crosses below
  `runway_min_units`.
- Balance-zero: broker disconnects RTMP cleanly; HLS finalized with EXT-X-ENDLIST.
- No-push timeout: session opened but no RTMP within `expires_at` → auto-close
  + full refund.
- Header validation on the session-open POST: same matrix as `http-reqresp`.

Fixtures: `conformance/fixtures/rtmp-ingress-hls-egress/*.yaml`.

## Changelog

| Mode version | Date | Change |
|---|---|---|
| 0.1.0 | 2026-05-06 | Initial draft. |
| 0.1.1 | 2026-05-06 | Add `stream_key` field to the session-open response (32-byte URL-safe random bearer). URL shape moves to path-based `rtmp://host:1935/<session_id>/<stream_key>`; query-string variants rejected. Session-end and idle-disconnect text refined for the LL-HLS layout. Pre-1.0 minor additions are non-breaking; receivers continue to validate the major version only. |
