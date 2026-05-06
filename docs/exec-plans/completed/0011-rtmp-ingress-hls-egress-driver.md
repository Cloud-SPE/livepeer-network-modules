# Plan 0011 — rtmp-ingress-hls-egress@v0 driver pair (session-open phase)

**Status:** completed (2026-05-06)
**Opened:** 2026-05-06
**Closed:** 2026-05-06
**Owner:** initial author + assistant

## Goal

Broker + runner drivers for the **session-open phase** of
`rtmp-ingress-hls-egress@v0`. The broker accepts the session-open POST,
returns 202 with the required URL set; the runner asserts the wire shape.

## Scope (deliberately narrow)

In scope:

- Broker driver: handle `POST /v1/cap` for `rtmp-ingress-hls-egress@v0`.
  Generate `session_id`, return 202 with `rtmp_ingest_url`,
  `hls_playback_url`, `control_url`, `expires_at`. URLs are derived from
  `Capability.Backend.URL` + a generated session-id; they do not have to
  resolve to live listeners in v0.1.
- Runner driver: send the session-open POST, assert 202 + body fields
  present + URL string formats.
- Fixture: `fixtures/rtmp-ingress-hls-egress/happy-path.yaml`.
- Test-config capability declaration.

Out of scope (deferred to a future plan):

- Actual RTMP listener on the broker.
- FFmpeg subprocess management for transcoding.
- HLS sink (filesystem or S3).
- The runner pushing real video frames over RTMP.
- The runner verifying HLS playlist + segments.
- Cadence-based debit during the live session.
- Control-plane WebSocket events (`session.balance.low`, etc.).
- `expires_at` enforcement / no-push timeout.

The full media pipeline becomes its own plan (0011a or similar) once the
spec shape is locked at the wire level.

## Why narrow

RTMP listeners + FFmpeg subprocess management + HLS sinks are
infrastructure-heavy. Locking the session-open wire shape first lets the
gateway side (plan 0009) build against a known broker contract; the media
pipeline implementation can land independently without breaking anything
that already passes conformance.

## Done condition

`make test-compose` from `conformance/` exits 0 with the v0.1 fixture set
+ rtmp-ingress-hls-egress/happy-path.yaml.
