---
plan: 0026
title: broker-paid / worker-media split for live video — draft
status: draft
phase: plan-only
opened: 2026-05-11
owner: harness
related:
  - "completed plan 0011 — rtmp-ingress-hls-egress driver"
  - "completed plan 0011-followup — rtmp-ingress-hls-egress media pipeline"
  - "completed plan 0015 — interim-debit cadence on long-running modes"
  - "completed plan 0016 — chain-integrated payment-daemon — design choices"
  - "completed plan 0018 — orch-coordinator design"
  - "video-gateway/docs/operator-runbook.md — current active product model"
audience: broker maintainers, gateway maintainers, video operators
---

# Plan 0026 — broker-paid / worker-media split for live video (draft)

> **Draft only. Not active.** This document describes a possible future
> live architecture in which the broker remains the payment and session
> authority while the media plane runs on a separate worker. No code,
> protocol, or schema changes are implied by this draft.

## 1. Problem

The shipped live path is broker-native:

1. gateway opens `video:live.rtmp` at the broker,
2. broker validates payment,
3. broker accepts RTMP directly,
4. broker runs `ffmpeg`,
5. broker serves HLS.

That design keeps payment enforcement local to the service endpoint,
but it couples:

- payment admission,
- session authority,
- RTMP ingress,
- transcoding,
- HLS serving

onto the same host class and the same broker image.

Operators who want dedicated media workers instead of broker-side
transcoding need a split design:

- broker remains the paid control plane,
- worker becomes the media plane.

## 2. Target invariant

For every live session:

1. the gateway pays and opens the session against the broker,
2. the broker chooses the worker and remains the billing authority,
3. the worker accepts media only for broker-authorized sessions,
4. the worker reports usage and lifecycle state back to the broker,
5. the broker commits or terminates the session based on payment runway.

The worker must never become an independent payment authority.

## 3. Proposed shape

The live capability remains broker-advertised and broker-selected, but
session-open returns a worker handoff instead of a broker-local media
endpoint.

Control plane:

- gateway ↔ broker
- worker ↔ broker

Media plane:

- gateway ↔ worker
- viewer ↔ worker or viewer ↔ gateway-backed playback origin

The broker keeps:

- payment validation,
- route selection,
- session ID allocation,
- stream-key authority,
- usage settlement,
- forced termination rights.

The worker keeps:

- RTMP ingest,
- `ffmpeg` subprocess ownership,
- HLS packaging,
- media-plane health reporting.

## 4. Proposed API flow

### 4.1 Session-open: gateway → broker

Request:

`POST /v1/cap`

Headers:

- `Livepeer-Capability: video:live.rtmp`
- `Livepeer-Offering: <offering>`
- payment envelope headers already defined in the current wire

Body:

```json
{
  "caller_id": "proj_123",
  "stream_id": "live_abcd1234"
}
```

Broker actions:

1. validate payment / open receiver session,
2. select a concrete media worker,
3. allocate `session_id`,
4. mint `stream_key`,
5. mint a short-lived broker-signed worker handoff token,
6. persist broker-side session state:
   - `session_id`
   - `stream_id`
   - `worker_id`
   - `worker_media_base_url`
   - `payer identity`
   - `last_debit_seq`
   - `status=open`

Response:

```json
{
  "session_id": "sess_01",
  "stream_key": "sk_live_xxx",
  "rtmp_ingest_url": "rtmp://worker-a:1935/sess_01/sk_live_xxx",
  "hls_playback_url": "https://worker-a/_hls/sess_01/master.m3u8",
  "worker_control_url": "https://worker-a/v1/live/ingest",
  "worker_id": "worker-a",
  "broker_session_token": "<signed-handoff-token>",
  "expires_at": "2026-05-11T14:00:00Z"
}
```

Notes:

- `rtmp_ingest_url` stays path-shaped for encoder compatibility.
- `broker_session_token` is for worker authorization, not for the
  customer encoder.

### 4.2 Session-bind: worker ← broker-signed handoff

Two variants are possible.

Variant A: encoded in RTMP URL path only.

- worker receives `session_id` and `stream_key` from RTMP path,
- worker calls broker to validate them before starting media.

Variant B: explicit pre-bind.

Gateway calls:

`POST https://worker-a/v1/live/ingest/session-bind`

Body:

```json
{
  "session_id": "sess_01",
  "stream_id": "live_abcd1234",
  "stream_key": "sk_live_xxx",
  "broker_session_token": "<signed-handoff-token>"
}
```

Worker verifies:

1. token signature,
2. token expiry,
3. token audience matches this worker,
4. `session_id` / `stream_id` / `stream_key` match the token claims.

Recommendation: Variant B. It avoids making worker authorization depend
on RTMP parser extensions and provides a clean place for worker-side
admission errors before media begins.

Response:

```json
{
  "status": "bound",
  "rtmp_ingest_url": "rtmp://worker-a:1935/sess_01/sk_live_xxx",
  "hls_playback_url": "https://worker-a/_hls/sess_01/master.m3u8"
}
```

### 4.3 Media ingress: gateway → worker

After bind succeeds, the gateway proxies customer RTMP to the worker.

Worker actions:

1. accept RTMP publish for the bound `session_id`,
2. start `ffmpeg`,
3. package HLS,
4. expose HLS at the playback URL,
5. start emitting usage checkpoints to the broker.

### 4.4 Usage checkpoints: worker → broker

Worker posts periodic signed updates:

`POST /v1/live/sessions/{session_id}/usage-checkpoint`

Body:

```json
{
  "worker_id": "worker-a",
  "sequence": 7,
  "out_time_seconds": 210,
  "bytes_in": 41892312,
  "ffmpeg_state": "running",
  "observed_at": "2026-05-11T13:30:00Z",
  "signature": "<worker-signature>"
}
```

Broker actions:

1. verify worker identity,
2. enforce monotonic sequence,
3. compute usage delta since prior checkpoint,
4. run interim debit against payment-daemon,
5. decide whether session remains funded.

Response:

```json
{
  "status": "ok",
  "continue": true,
  "next_min_runway_seconds": 60
}
```

If balance is exhausted:

```json
{
  "status": "insufficient_balance",
  "continue": false,
  "terminate_reason": "runway_exhausted"
}
```

### 4.5 Broker termination: broker → worker

When balance is exhausted or the gateway closes the session, the broker
must be able to terminate media explicitly:

`POST https://worker-a/v1/live/ingest/session-close`

Body:

```json
{
  "session_id": "sess_01",
  "reason": "runway_exhausted",
  "broker_signature": "<broker-signature>"
}
```

Worker actions:

1. verify broker authorization,
2. stop RTMP publish acceptance,
3. terminate `ffmpeg`,
4. finalize checkpoint,
5. mark session closed.

### 4.6 Final settlement: worker → broker

Worker posts the terminal receipt:

`POST /v1/live/sessions/{session_id}/complete`

Body:

```json
{
  "worker_id": "worker-a",
  "final_sequence": 9,
  "final_out_time_seconds": 244,
  "status": "completed",
  "ended_at": "2026-05-11T13:30:34Z",
  "signature": "<worker-signature>"
}
```

Broker actions:

1. verify terminal receipt,
2. flush final debit delta,
3. close payment-daemon session,
4. mark broker session closed,
5. expose final billing state to the gateway.

## 5. Trust and auth model

### 5.1 Worker authorization

Workers must accept live media only for broker-authorized sessions.
Allowed patterns:

- broker-signed JWT/JWS handoff tokens,
- mTLS + broker callback validation,
- detached signature over session claims.

Draft recommendation: broker-signed short-lived handoff token scoped to:

- `session_id`
- `stream_id`
- `worker_id`
- `stream_key`
- expiry

### 5.2 Worker identity

Broker must know which worker is reporting usage. Viable bindings:

- static operator-managed worker keypairs,
- mTLS client certificates,
- worker identity asserted by a session-runner-style launch authority.

Draft recommendation: mTLS or detached Ed25519 signatures per worker.

### 5.3 Usage authority

In the shipped broker-native live path, the broker observes usage
locally. In the split design, usage becomes worker-reported. That is a
strictly weaker trust position unless:

- worker identity is strong,
- reports are monotonic,
- the broker can challenge or terminate on inconsistency.

The design therefore preserves the current v1 trust model:

- trust the worker's reported usage in v1,
- reserve hooks for verifiable receipts later.

## 6. Failure handling

### 6.1 Worker never binds

If the broker opened a paid session but the worker never confirms bind:

- broker expires the session after a short bind timeout,
- no debits are committed,
- gateway receives a session-open failure or immediate closure.

### 6.2 Worker bound, no media arrives

If bind succeeded but RTMP publish never starts:

- worker reports `bound_idle`,
- broker closes after idle timeout,
- no usage beyond any minimal session fee is committed.

### 6.3 Worker keeps running after balance exhaustion

Broker sends `session-close`.

If worker does not comply:

- broker marks worker unhealthy,
- future routing suppresses that worker,
- session is closed at the broker accounting layer anyway.

### 6.4 Broker unavailable during live stream

Open question:

- should worker continue briefly using the last known runway,
- or terminate immediately on checkpoint failure?

Draft recommendation: bounded grace window with a small local buffer,
then terminate if broker connectivity does not recover.

## 7. Files and components a future active plan would touch

This draft does not authorize edits. A future active plan would likely
touch:

### `video-gateway/`

- live session-open client
- RTMP proxy / adapter flow
- playback policy assumptions

### `capability-broker/`

- live session-open response shape
- worker selection for live media
- broker-side session store
- worker checkpoint / close endpoints
- interim-debit integration against worker-reported deltas

### `video-runners/` or new live worker component

- live ingress bind endpoint
- RTMP listener
- `ffmpeg` lifecycle
- HLS serving
- usage checkpoint client

### `livepeer-network-protocol/`

- mode update or new mode for split live handoff
- signed handoff token schema
- worker checkpoint payload schema

## 8. Open questions

1. Is HLS playback served directly by the worker, or does the worker
   publish into shared storage and let gateway/CDN serve playback?
2. Does the gateway perform an explicit bind call, or is worker
   authorization implicit in the RTMP path + broker callback?
3. What exact identity mechanism signs worker usage checkpoints?
4. Should the worker be able to continue for a bounded grace period when
   the broker is temporarily unreachable?
5. Does `video:live.rtmp` stay the capability ID, or does this become a
   sibling mode/capability split from the broker-native live path?

## 9. Non-goals

- replacing the shipped broker-native live path now
- removing the broker from payment processing
- designing verifiable cryptographic media receipts in v1
- defining chain publication or manifest changes beyond what is needed
  to describe a live-capable worker route

## 10. Recommendation

This architecture is viable if the operator goal is:

- keep payment and admission in the broker,
- move media CPU/GPU work off the broker host.

The cost is real protocol complexity:

- broker-issued worker handoff,
- worker-reported usage,
- explicit broker-driven termination,
- more split-brain failure cases.

If an operator only needs the shipped design to work, the simpler path
remains:

- keep live broker-native,
- bake `ffmpeg` into the broker image,
- run dedicated broker hosts for live media workloads.

If operators repeatedly ask for separate media workers while preserving
broker-side billing authority, promote this draft into `active/` and
turn §4 into a concrete protocol plan.
