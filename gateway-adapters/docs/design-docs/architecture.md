# Architecture

Cross-language package layout for `gateway-adapters/`.

## Layout

```
gateway-adapters/
├── ts/                                 # TypeScript half (npm)
│   ├── src/
│   │   ├── headers.ts                  # canonical Livepeer-* names + ERROR_CODE constants
│   │   ├── errors.ts                   # LivepeerBrokerError + errorFromResponse
│   │   ├── types.ts                    # BrokerEndpoint + BrokerCall + BrokerResponseEnvelope
│   │   ├── modes/
│   │   │   ├── http-reqresp.ts         # send(endpoint, req) → response
│   │   │   ├── http-stream.ts          # uses node:http for trailer access
│   │   │   ├── http-multipart.ts
│   │   │   ├── ws-realtime.ts          # connect(endpoint, req) → WS-shaped object
│   │   │   ├── session-control-plus-media.ts
│   │   │   │                           # session-open + control-WS event emitter
│   │   │   └── index.ts
│   │   ├── payer-daemon.ts             # GetSessionDebits gRPC client
│   │   └── index.ts
│   └── test/                           # node:test files; one per mode
└── go/                                 # Go module (importable)
    ├── headers/headers.go              # mirrors ts/src/headers.ts
    ├── errors/errors.go                # mirrors ts/src/errors.ts
    ├── modes/
    │   ├── rtmpingresshlsegress/       # RTMP listener + customer→broker relay
    │   └── sessioncontrolplusmedia/    # WebRTC SFU pass-through
    └── internal/                       # session map, listener wiring
```

## Per-mode dispatch

Each TS-half mode exports a `send(endpoint, req)` (or for long-lived
modes, `connect(endpoint, req)`) function with mode-specific `Request`
and `Response` types. The function:

1. Builds the five required `Livepeer-*` request headers (Capability,
   Offering, Payment, Spec-Version, Mode) plus optional
   `Livepeer-Request-Id`.
2. Invokes the appropriate transport (`fetch` for HTTP-reqresp;
   `node:http` for `http-stream` to access trailers; `ws` for
   `ws-realtime` and the control-WS half of
   `session-control-plus-media`).
3. Reads response headers / body / trailers / events; throws
   `LivepeerBrokerError` on non-2xx with the structured `Livepeer-Error`
   code, optional `Livepeer-Backoff` advice, and `Livepeer-Request-Id`.
4. Returns a typed response object (HTTP modes) or a `WebSocket`-shaped
   object / event emitter (long-lived modes) with `workUnits`,
   `requestId`, body bytes, and headers / events.

The Go-half modes are session-shaped: each exposes a `Listener` (RTMP)
or a `Mediator` (WebRTC SFU) that the gateway wires into its accept
loop.

## What this library does NOT do

- **Resolver.** Caller resolves a route via `service-registry-daemon`
  before invoking the middleware. The resolved `worker_url` becomes
  `BrokerEndpoint.url`.
- **Payment-daemon (mint side).** Caller mints the base64-encoded
  `Livepeer-Payment` envelope before invoking the middleware (passed
  via `paymentBlob`). On session close the adapter MAY consult the
  payer-daemon's session ledger via `PayerDaemon.GetSessionDebits` to
  surface a final work-units count, but the mint path is the
  gateway's.
- **Customer auth.** Gateway-level concern; not part of this library.
- **Mode dispatch from a single entry-point.** Callers pick the right
  per-mode `send` / `connect` / `Listener` / `Mediator`; no runtime
  mode dispatcher in this layer.

## Module strategy

- **TS half:** ESM-only (`"type": "module"`). Top-level + per-mode
  subpath exports (`./modes/http-reqresp` etc.) so consumers can
  import only what they need.
- **Go half:** sub-module
  `github.com/Cloud-SPE/livepeer-network-rewrite/gateway-adapters/go`.
  Per-mode packages so consumers `import` only the modes they use.
