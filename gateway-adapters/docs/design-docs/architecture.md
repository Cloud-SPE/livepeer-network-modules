# Architecture

Planned package layout for `@tztcloud/livepeer-gateway-middleware`.

## Layout

```
gateway-adapters/
├── src/
│   ├── headers.ts        # canonical Livepeer-* names + ERROR_CODE constants
│   ├── errors.ts         # LivepeerBrokerError + errorFromResponse
│   ├── types.ts          # BrokerEndpoint + BrokerCall + BrokerResponseEnvelope
│   ├── modes/
│   │   ├── http-reqresp.ts    # send(endpoint, req) → response
│   │   ├── http-stream.ts     # uses undici.request for trailer access
│   │   ├── http-multipart.ts
│   │   └── index.ts           # namespace re-exports
│   └── index.ts          # public surface (re-exports)
└── test/                 # node:test files; one per mode
```

## Per-mode dispatch

Each mode exports a `send(endpoint, req)` function with mode-specific
`Request` and `Response` types. The function:

1. Builds the five required `Livepeer-*` request headers (Capability,
   Offering, Payment, Spec-Version, Mode) plus optional `Livepeer-Request-Id`.
2. Invokes the appropriate HTTP client (`fetch` for non-trailer modes,
   `undici.request` for `http-stream` to access trailers).
3. Reads response headers / body / trailers; throws
   `LivepeerBrokerError` on non-2xx with the structured `Livepeer-Error`
   code, optional `Livepeer-Backoff` advice, and `Livepeer-Request-Id`.
4. Returns a typed response object with `workUnits`, `requestId`, body
   bytes, and headers.

## What this library does NOT do

- **Resolver.** Caller resolves a route via `service-registry-daemon`
  before invoking the middleware. The resolved `worker_url` becomes
  `BrokerEndpoint.url`.
- **Payment-daemon.** Caller mints the base64-encoded `Livepeer-Payment`
  envelope before invoking the middleware (passed via `paymentBlob`).
- **Customer auth.** Gateway-level concern; not part of this library.
- **Mode dispatch from a single entry-point.** Callers pick the right
  per-mode `send` function; no runtime mode dispatcher in this layer.

## Module strategy

ESM-only (`"type": "module"`). Top-level + per-mode subpath exports
(`./modes/http-reqresp` etc.) so consumers can import only what they
need without dragging in `undici` if they don't use `http-stream`.

Zero runtime dependencies: only Node built-ins (`fetch`, `undici`,
`node:http`).
