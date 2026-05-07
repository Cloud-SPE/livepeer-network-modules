# DESIGN — TS half

Component-local design summary for the TypeScript half of
`gateway-adapters/`. The Go half (RTMP listener + WebRTC SFU pass-through)
lives at [`../go/`](../go/); cross-language design notes live at
[`../DESIGN.md`](../DESIGN.md). Cross-cutting design lives at the repo
root in [`../../docs/design-docs/`](../../docs/design-docs/).

## What this component is

A TypeScript library that any gateway can import to:

1. Build the five required `Livepeer-*` request headers + the optional
   `Livepeer-Request-Id` correlation header.
2. Send a paid request to a capability-broker via the appropriate mode
   driver.
3. Read response headers (`Livepeer-Work-Units`, `Livepeer-Backoff`, etc.)
   and surface broker errors via `LivepeerBrokerError`.

The TS half covers `http-reqresp@v0`, `http-stream@v0`,
`http-multipart@v0`, `ws-realtime@v0`, and the control-WS surface of
`session-control-plus-media@v0`. The RTMP listener and WebRTC media plane
live in the Go half because those workloads do not run on Node in
production.

## What it is not

- **Not a runtime service.** It's a library imported into a gateway
  service. Per core belief #15, services ship as Docker images;
  libraries ship as packages.
- **Not the resolver.** The caller resolves a route (capability, offering,
  worker_url) before invoking the middleware.
- **Not the payment-daemon.** The caller mints the `Livepeer-Payment`
  envelope (base64 protobuf) before invoking the middleware. For
  long-lived sessions the adapter may consult the payer-daemon's
  per-session debit ledger to surface a final work-units count on
  close, but the payment-mint path is owned by the gateway.

## Wire-spec compliance

Implements the protocol at
[`../../livepeer-network-protocol/`](../../livepeer-network-protocol/):

- [`headers/livepeer-headers.md`](../../livepeer-network-protocol/headers/livepeer-headers.md)
  defines what request headers we set and what response headers / trailers we
  read.
- Per-mode shapes:
  - [`modes/http-reqresp.md`](../../livepeer-network-protocol/modes/http-reqresp.md)
    — implemented in `src/modes/http-reqresp.ts`.
  - [`modes/http-stream.md`](../../livepeer-network-protocol/modes/http-stream.md)
    — implemented in `src/modes/http-stream.ts`. Uses Node's built-in
    `node:http` / `node:https` for trailer access (the standard `fetch`
    API does not expose response trailers).
  - [`modes/http-multipart.md`](../../livepeer-network-protocol/modes/http-multipart.md)
    — implemented in `src/modes/http-multipart.ts`.
  - [`modes/ws-realtime.md`](../../livepeer-network-protocol/modes/ws-realtime.md)
    — implemented in `src/modes/ws-realtime.ts`.
  - [`modes/session-control-plus-media.md`](../../livepeer-network-protocol/modes/session-control-plus-media.md)
    — control-WS surface implemented in `src/modes/session-control-plus-media.ts`.
    Media-plane provisioning (WebRTC SFU pass-through) lives in the Go
    half at `../go/modes/sessioncontrolplusmedia/`.

## Internal architecture

See [`docs/design-docs/architecture.md`](./docs/design-docs/architecture.md)
for the planned package layout and request lifecycle.

## Dependencies

**Runtime:** `ws` (Node's de-facto WebSocket library; Node has no
built-in WebSocket client) and `@grpc/grpc-js` (only when the adapter
talks to the payer-daemon for final-debit lookup; pulled in transitively
via the existing reference gateway). The HTTP-family modes remain
zero-dep on Node built-ins.

**Dev only:** `typescript`, `@types/node`, `@types/ws`. Tests use
`node:test`.
