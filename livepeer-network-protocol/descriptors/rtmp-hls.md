---
schema_name: rtmp-hls
tag: rtmp-hls/v1
version: 1.0.0-draft
status: draft
last_updated: 2026-08-18
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
