---
schema_name: rtmp-hls
tag: rtmp-hls/v1
version: 1.1.0-draft
status: draft
last_updated: 2026-09-09
---

# Descriptor schema: `rtmp-hls/v1`

A runner-owned live-video runtime: RTMP ingest in, HLS playback out. The
legacy remote-runner wire contract, re-expressed as one descriptor schema
among peers — with the one structural change that stream keys are no longer
returned in the open response but issued by the gateway via a grant.

Typical offering axes: `attachment: external`, `metering: runner-reported`,
work unit `output_seconds`.

## Public fields

| Field | Req | Customer-safe | Meaning |
|---|---|---|---|
| `rtmp_url` | yes | yes | RTMP ingest endpoint publishers push to. |
| `hls_url` | yes | yes | HLS master-playlist URL for playback. Doubles as the verifiability hook: an advancing playlist is proof of service. |
| `key_issue_url` | yes | no | Runner endpoint where the gateway presents the grant to issue or rotate stream keys. Gateway-only. |
| `status_url` | no | no | Optional session-health probe. |

## Private fields

None required.

## Grants

Exactly one:

| Operation | `max_uses` | Meaning |
|---|---|---|
| `stream-key-issue` | absent (unbounded) | The gateway presents the grant secret at `key_issue_url` to issue a stream key (and reissue on rotation or publisher churn), scoped to this runner session. |

Moving key issuance behind a grant is what puts ingest admission at the
gateway's edge: the gateway knows first-hand which customer received a key
and when publishing became possible, which is its meter of record
(wall-clock stream time, cross-checked against the `hls_url` probe). The
legacy behavior — plaintext key in the open response — is gone; its
"returned once only" rule survives as the grant-delivery rule itself.

## Conformance (public-by-contract)

`rtmp_url`, `hls_url`, `key_issue_url`, `status_url` — and nothing else —
may appear in this schema's public part. Leak fixtures assert an issued
stream key and the grant secret never surface in any broker response,
including status after issuance.

## Output-health events

An `rtmp-hls/v1` runner implements paid-session's optional output-health
details. Heartbeats and output-health transitions carry `output_state`,
`output_state_since`, and the last safe `last_failure_code` when one exists.

The schema defines two additional open-world event names:

| Event | Required details | Meaning |
|---|---|---|
| `session.ladder.restart` | `code`, `attempt`, `output_state` | A rendition ladder failed and the runner began a bounded retry. `attempt` is a positive counter in the runner's bounded retry window. |
| `session.output.stalled` | `output_state: stalled`, `output_state_since`, optional `last_failure_code` | Authenticated ingest expected output, but no complete metering-rendition segment advanced by the runner's stall deadline. |

Safe ladder failure codes are `encoder_init_failed`, `hwaccel_init_failed`,
`input_unavailable`, `output_rejected`, `process_killed`, and `unknown`.
Diagnostics never cross this contract. A runner that reaches its output failure
deadline emits terminal `session.failed` with `close_reason: output_failed` and
its final cumulative `output_seconds` claim. The broker preserves that reason
and performs the standard fail-closed winddown.

The runner owns its ingest-to-output deadline; the broker's paid-session
60-second continuous-stall rule is an independent backstop. For certification,
a 75-second authenticated publish must report at least 60 `output_seconds`, or
terminate as `output_failed` with a safe failure code within 60 seconds of
authenticated ingest.

## Changelog

| Version | Date | Change |
|---|---|---|
| 1.1.0-draft | 2026-09-09 | Defines output-health details, ladder restart and output stalled events, safe ladder failure codes, and fail-closed live-publish certification behavior. |
| 1.0.0-draft | 2026-08-18 | Initial RTMP ingest and HLS playback descriptor. |
