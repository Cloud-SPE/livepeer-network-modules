---
title: Interaction modes
status: active
last-reviewed: 2026-05-15
---

# Interaction modes

Cross-cutting contract for the finite set of gateway↔broker wire shapes in the
rewrite.

The architectural rule is simple:

- capabilities are workload-specific
- interaction modes are not
- gateways and the broker implement modes once and reuse them across many
  capabilities

This is the control-plane seam that keeps the stack workload-agnostic.

## Why modes exist

The broker and gateways must agree on:

- how a session is opened
- whether the response is one-shot or long-lived
- where billing starts and ends
- whether the media plane is in-band, broker-relayed, or out-of-band

Capabilities should not redefine those rules independently. They choose from a
small fixed typology instead.

## Current mode set

| Mode | Shape | Typical capabilities |
|---|---|---|
| `http-reqresp@v0` | one HTTP request → one HTTP response | embeddings, image generation, VOD dispatch, generic REST |
| `http-stream@v0` | HTTP request → SSE/chunked stream | chat completions streaming |
| `http-multipart@v0` | multipart upload → one HTTP response | audio transcription |
| `ws-realtime@v0` | bidirectional WebSocket session | realtime OpenAI-style sessions, control streams |
| `rtmp-ingress-hls-egress@v0` | RTMP ingest → LL-HLS playback | live video |
| `session-control-plus-media@v0` | HTTP session open + broker-managed control/media runtime | VTuber-style session runtimes |
| `session-control-external-media@v0` | HTTP session open + broker-managed control/payment, external media plane | Daydream Scope |

## Mode responsibilities

Every mode defines:

- the canonical broker entrypoint path
- required request headers and session identifiers
- session-open and close semantics
- whether payment is one-shot or session-based
- what the broker meters locally
- what is returned to the gateway/customer as the next-hop surface

Every mode does **not** define:

- capability naming
- product pricing policy
- health semantics beyond the generic stack contracts
- workload-specific backend behavior

Those stay in manifests, broker config, and product-facing gateways.

## Request/response family

### `http-reqresp@v0`

Use when one inbound request maps to one backend response.

Properties:

- one route choice
- one payment envelope
- one broker forward
- one usage extraction step
- one response body back to the gateway

Typical fit:

- embeddings
- image generation
- generic REST capability wrappers
- offline video job dispatch

### `http-stream@v0`

Use when one inbound request produces a streamed HTTP response.

Properties:

- one route choice
- one payment envelope
- one broker forward
- long-lived response body
- usage reconciled at stream completion or terminal accounting point

Typical fit:

- streamed chat completions

### `http-multipart@v0`

Use when the customer uploads files or mixed form-data and expects one final
response.

Typical fit:

- audio transcription
- image/audio upload APIs

## Long-lived/session family

These modes follow the worker-metered / gateway-ledger split from
[streaming-workload-pattern.md](./streaming-workload-pattern.md).

### `ws-realtime@v0`

Use when the customer contract is a long-lived bidirectional WebSocket.

Properties:

- gateway opens a realtime session against the broker
- broker owns runtime debit cadence
- control and data travel on the same WS plane

Typical fit:

- realtime LLM/audio sessions

### `rtmp-ingress-hls-egress@v0`

Use when the customer publishes RTMP and consumes LL-HLS output.

Properties:

- session-open returns ingest/playback coordinates
- broker owns live runtime state and usage metering
- media plane is broker-managed

Typical fit:

- live video ingest and playback

### `session-control-plus-media@v0`

Use when the broker owns both the control plane and the media/session runtime
for a long-lived workload.

Properties:

- session-open returns broker-hosted control/media coordinates
- broker may spawn or bind to a per-session backend runtime
- broker relays or directly manages the media/control plane

Typical fit:

- VTuber session workloads
- future broker-hosted interactive runtimes

### `session-control-external-media@v0`

Use when the broker owns payment and session authority, but the workload keeps
its own external media plane.

Properties:

- broker still validates payment and meters the live session
- session-open returns broker-hosted control URLs plus a broker-mediated
  passthrough URL into the workload's native API
- media does not flow through the broker

Typical fit:

- Daydream Scope
- future long-lived runtimes with their own TURN/WebRTC/media stack

## How a new capability picks a mode

Ask:

1. Is this one request / one response, streamed response, multipart upload, WS,
   live media ingest, broker-managed session, or external-media session?
2. Can an existing mode express the full customer↔gateway↔broker contract?
3. If yes, add YAML/config and product routing only.
4. If no, a new interaction mode is a cross-stack protocol change and needs a
   plan.

That is the key cost boundary:

- new capability under an existing mode: cheap
- new interaction mode: expensive and deliberate

## Cross-stack implications

- `capability-broker` owns one driver per mode
- gateways own one adapter per mode
- `service-registry-daemon` routes on manifest/live health, not on mode
  semantics
- `payment-daemon` remains mode-agnostic; only the caller's lifecycle differs

## See also

- [architecture-overview.md](./architecture-overview.md)
- [streaming-workload-pattern.md](./streaming-workload-pattern.md)
- [payment-daemon-interactions.md](./payment-daemon-interactions.md)
- [`../../livepeer-network-protocol/modes/`](../../livepeer-network-protocol/modes/)
