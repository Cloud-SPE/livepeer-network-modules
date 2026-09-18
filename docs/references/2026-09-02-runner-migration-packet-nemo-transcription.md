# Runner migration packet — `audio-diarized-transcription-runner` (NeMo)

Date: 2026-09-02. For the author of
`shane-demo/audio-diarized-transcription-runner`
(`moatus/audio-diarized-transcription-runner`). Based on a read of the
repository on 2026-09-01/02; quoted paths are from your source. Tracked as
`lnm-yqc`.

**Dependency order (decision 4):** the batch half waits on nothing and is
independently verifiable. The streaming half depends on
`pcm-transcript/v1`, which has landed in `livepeer-network-protocol`
(`descriptors/pcm-transcript.md`), and on the pool's member-edge feature
(`lnm-7cj`, after plan 0045) before a member can be placed on it — but the
runner's side of it can be built and verified against a broker now.

## The short version

One image, two capabilities, two containers. **Batch** (`POST
/v1/audio/transcriptions`) stays `openai:audio-transcriptions` and is close
to done: serve the contract, report the logical model id as identity. **True
streaming** (`WS /v1/audio/transcriptions/stream`) becomes
`audio:transcribe.live`, a `paid-session/v1` runner emitting the
`pcm-transcript/v1` descriptor — it is not an OpenAI endpoint and it no
longer shares an id with the batch one (decision 6). Each container declares
which it is by `CAPABILITY_NAME`.

The HTTP "live sessions" surface (`…/diarized-transcriptions/live/sessions`)
is **not sold** by the pool (decision 13): true streaming is in scope,
chunked upload is not. Keep or delete it as you like.

## What you can delete

| What you have | Why it existed | What replaces it |
|---|---|---|
| `GET /openai:audio-transcriptions/options` | The v0 broker dialled runners | `GET /.well-known/livepeer-runner`. |
| `streaming_modes` vocabulary in the options payload | Mode negotiation | `transports` (batch) / `descriptor_schemas` (streaming). |
| Socket query strings `?language=&preset=&session_id=` on the stream | No session existed to configure | `session_params` at create; the socket is per-session and already configured (below). |

## What changes — batch

```json
{
  "capability_id": "openai:audio-transcriptions",
  "protocol": "paid-job/v1",
  "transports": ["multipart"],
  "work_unit": { "name": "audio_seconds",
                 "extractor": { "type": "response-header", "name": "X-Livepeer-Work-Units" } },
  "paths": { "invoke": "/v1/audio/transcriptions" },
  "readiness": { "type": "http-status", "path": "/healthz" },
  "identity": { "openai.model": "nemo-diarized-transcription-meeting-v0", "provider": "nemo" },
  "schema_versions": { "paid-job/v1": "1.0.15" }
}
```

`identity.openai.model` is the **logical** id
`nemo-diarized-transcription-meeting-v0` — what the catalog matches on and
what a caller sends as `"model"` — not the underlying
`parakeet-tdt-0.6b-v3`. You already emit `X-Livepeer-Work-Units`.

## What changes — streaming

The runner becomes a `paid-session/v1` runner. Read
`protocols/paid-session.md` §11 (the implementer's checklist) and
`descriptors/pcm-transcript.md`; in brief:

**Contract:**

```json
{
  "capability_id": "audio:transcribe.live",
  "protocol": "paid-session/v1",
  "descriptor_schemas": ["pcm-transcript/v1"],
  "metering": "runner-reported",
  "work_unit": { "name": "audio_seconds" },
  "paths": { "create": "/v1/sessions", "status": "/v1/sessions/{id}", "terminate": "/v1/sessions/{id}" },
  "readiness": { "type": "http-status", "path": "/healthz" },
  "identity": { "model": "nemotron-speech-streaming-en-0.6b", "provider": "nemo" },
  "heartbeat": { "interval_seconds": 5 },
  "session_params_schema": { "type": "object", "properties": { "language": { "type": "string" } } },
  "schema_versions": { "paid-session/v1": "1.0.11", "pcm-transcript/v1": "1.0.0" }
}
```

`identity.model` (plain key — not an OpenAI capability) is your own model
string; the catalog matches `nemotron-speech-streaming-en-0.6b` and carries
the `nvidia/` HF path in `extra.backend_model`.

**Session lifecycle** (the broker drives it over the agent's tunnel):

1. `POST` create with `session_params` (`language`, your `preset`, …) and
   the broker's callback URL and token. You return `runner_session_id` and
   the descriptor:
   ```json
   { "runner_session_id": "…",
     "runtime": { "schema": "pcm-transcript/v1",
                  "public": { "url": "wss://<public host>/v1/sessions/<id>/stream",
                              "audio": { "encoding": "pcm_s16le", "sample_rate_hz": 16000, "channels": 1 } },
                  "grants": [ { "operation": "stream-attach", "secret": "…" } ] } }
   ```
   The `url` is per session. The `public host` is the address the pool
   member agent hands you at start (`lnm-7cj`); until that feature lands,
   advertise what you are configured with.
2. The caller connects to `url` with `Authorization: Bearer <grant secret>`.
   Your existing socket protocol is the wire — PCM16 LE binary frames in,
   `event_type` JSON out, `{"type":"finish"}` / `{"type":"ping"}` control —
   with two changes: no query strings (the session is configured), and the
   grant on the upgrade. One live connection per session; resume on
   reconnect.
3. Report usage to the broker's callback as **seconds of audio ingested**
   (bytes ÷ 2 ÷ 16000), cumulative, at least every heartbeat interval.
   Silence costs nothing; a burst of a recording costs the recording.
4. `GET` status; `DELETE` terminate — end the socket with code 1000 and
   the final usage.

**Why not the HTTP live-sessions surface:** it maps onto
create/status/terminate almost verbatim, and the pool could relay it over
HTTP today. The operator chose true streaming (decision 13); sub-second
segments and live speaker changes are the product.

## What is *not* fixed by this work

- **The member's public endpoint.** `attachment: external` means the caller
  reaches your socket directly; a pool member behind NAT cannot be placed
  on this template until the pool's member-edge feature exists (`lnm-7cj`).
  Your side — grant on upgrade, per-session URL — does not depend on it.
- Certification cannot yet push audio through the socket, so the
  streaming template's usage step is non-required until it can.
- Image tag: the catalog omits `runner_compose.image` until you name the
  tag (`lnm-v12`).

## Verifying

Batch, now:

```sh
cd livepeer-network-protocol/conformance
go run ./cmd/livepeer-conformance --broker-url http://<broker>:8080 --serve-runner moatus/audio-diarized-transcription-runner:<tag>
```

Streaming: the same command with `CAPABILITY_NAME=audio:transcribe.live`
walks the paid-session scenarios (create, descriptor validation, usage
callback, heartbeat, terminate). The socket itself is exercised by the
member-edge feature's certification dial when it lands.

## What we need from you

1. The tag that serves the contract (both capabilities).
2. **Does the image run on `sm_61` / CUDA 12.x?** The batch template admits
   `rtx-2080` and up and no 1080 until you say yes (decision 9).
3. Your `session_params_schema` — which parameters the streaming session
   accepts at create.
