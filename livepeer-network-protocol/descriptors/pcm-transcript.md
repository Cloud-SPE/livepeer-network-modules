---
schema_name: pcm-transcript
tag: pcm-transcript/v1
version: 1.0.0-draft
status: draft
last_updated: 2026-09-02
---

# Descriptor schema: `pcm-transcript/v1`

A runner-hosted live transcription stream: the caller pushes raw PCM audio
over one WebSocket and receives transcript events on the same socket, for
as long as the session lives. The first workload to use it is diarized
meeting transcription (`audio:transcribe.live`); nothing in the schema is
specific to that model, and any live speech-to-text runner may emit it.

There is no external standard for this wire — unlike an SFU room, where
the participants speak WebRTC and the schema only has to hand over an
address — so this document defines the socket protocol as well as the
descriptor fields. A gateway that understands the tag can consume the
stream with no runner-specific knowledge.

Typical offering axes: `attachment: external`, `metering: runner-reported`.
The **work unit is an offering property, not a schema property**; the
natural unit is seconds of audio ingested, and §Metering says how a runner
measures it.

## Public fields

| Field | Req | Customer-safe | Meaning |
|---|---|---|---|
| `url` | yes | yes | The WebSocket endpoint for THIS session (`wss://…`). One session, one socket, one URL; the runner MUST NOT accept a second concurrent connection on it. |
| `audio` | yes | yes | The exact input format the runner accepts on binary frames: `{"encoding": "pcm_s16le", "sample_rate_hz": 16000, "channels": 1}`. v1 defines `pcm_s16le` only; a runner declares the rate and channel count it resamples nothing to. A caller MUST send this format and nothing else. |
| `status_url` | no | no | Verifiability hook: session health and ingest counters. Gateway-only; authorized by the same grant (below). Independent of the broker's configured `session.runner.status_path` — different caller, different credential. |

## Private fields

None required. Model API keys, internal service addresses, and other
long-lived runner credentials are runner-internal and MUST NOT appear
anywhere in the descriptor.

## Grants

Exactly one:

| Operation | `max_uses` | Meaning |
|---|---|---|
| `stream-attach` | absent (unbounded) | The grant secret authorizes the WebSocket upgrade at `url`, presented as `Authorization: Bearer <secret>`. Unbounded because a caller reconnects after a network fault; the runner MUST admit at most one live connection at a time and SHOULD resume, not restart, the transcript on reconnect. |
| `stream-status` | absent (unbounded) | The same grant authorizes probes of `status_url`. |

Both operations ride **one** grant, as in `sfu-room/v1`: the gateway is the
only holder of the secret, and a verifiability hook it cannot authenticate
against is not a hook. The grant dies with the session.

## Session parameters

What varies per session — the language, a diarization preset, a speaker
cap — is declared by the runner in `session_params_schema` (runner-attach
§3.2) and supplied by the buyer in `session_params` at open. The runner
applies them when it creates the session; **they are not query parameters
on the socket**. The socket a caller attaches to is already configured, so
a reconnect cannot silently change the transcript's language halfway
through. v1 reserves no parameter names; a runner that accepts none
declares an empty schema.

## Socket protocol

The socket carries two kinds of frame each way.

**Caller → runner**

- **Binary frames** are audio, in the `audio` format, in order, with no
  header. Frame size is the caller's choice; 20–500 ms of audio per frame
  is the useful range. A runner MUST accept any frame size up to 1 MiB.
- **Text frames** are JSON control messages with a `type`:
  - `{"type": "finish"}` — no more audio. The runner MUST flush, emit any
    remaining segments, emit `transcript.session.finished`, and close with
    code 1000. A finish is the caller's clean end; the session's paid life
    is still governed by the broker's lease and the gateway's close.
  - `{"type": "ping"}` — the runner answers `{"event_type": "pong"}`.

**Runner → caller**: text frames, JSON, each with an `event_type`. v1
defines these; a consumer MUST ignore an `event_type` it does not know, so
a runner may add its own without a new tag.

| `event_type` | When | Fields |
|---|---|---|
| `transcript.session.started` | once, after the upgrade is accepted | `session_id` |
| `transcript.segment` | as recognition produces text | `text` (string); `is_final` (bool — `false` is a partial that a later segment with the same `start` replaces); `start`, `end` (seconds of audio since the first frame); `speaker` (string, optional — present when the runner diarizes) |
| `speaker.update` | when the active speaker changes | `speaker` (string); `start` (seconds) |
| `transcript.session.finished` | once, after `finish` or when the runner ends the stream | `audio_seconds` (number — the runner's own ingest count, the same figure it reports as usage) |
| `pong` | in reply to `ping` | — |

**Errors** are a text frame `{"error": {"message": "…", "type": "…"}}`
followed by a close: 1008 for a rejected grant or malformed control, 1003
for audio the runner cannot accept, 1011 for a runner fault. A close for a
runner fault is the runner's failure for the session's outcome; a 1008 is
the caller's.

## Metering

The offering declares `metering: runner-reported`: **the runner is the
usage authority**, and its measure is audio ingested — bytes received on
binary frames divided by `2 × sample_rate_hz × channels` — reported as the
session's cumulative usage claim on the broker's usage callback. Wall-clock
connected time is not the meter: a caller who holds the socket open in
silence sends no bytes and is billed nothing, and a caller who bursts a
recording through at 10× real time is billed for the recording.

The gateway's own count of the bytes it forwarded is the first-party
cross-check the dual-meter model calls for, and what it may bill its own
customers from. That choice belongs to the gateway.

## Conformance (public-by-contract)

`url`, `audio`, `status_url` — and nothing else — may appear in this
schema's public part. The schema's leak fixtures assert the grant secret
and a model API key each fail to surface in any broker response.

## Changelog

| Version | Date | Change |
|---|---|---|
| 1.0.0-draft | 2026-09-02 | Initial schema (plan 0045, decisions 5 and 6 of the 2026-09-02 walkthrough). Written against the NeMo streaming runner's existing WebSocket: PCM16 LE 16 kHz mono in, `event_type` JSON events out, `finish`/`ping` control. Two deliberate departures from that runner as published: session parameters move from socket query strings to `session_params`, and the socket is per-session with a grant rather than a shared path with a `session_id` query. |
