# DESIGN

Component-local design summary. Cross-cutting design lives at the repo root in
[`../docs/design-docs/`](../docs/design-docs/).

## What this component is

A TypeScript library that any gateway can import to:

1. Build the five required `Livepeer-*` request headers + the optional
   `Livepeer-Request-Id` correlation header.
2. Send a paid request to a capability-broker via the appropriate mode
   driver.
3. Read response headers (`Livepeer-Work-Units`, `Livepeer-Backoff`, etc.)
   and surface broker errors via `LivepeerBrokerError`.

## What it is not

- **Not a runtime service.** It's a library imported into a gateway
  service. Per core belief #15, services ship as Docker images;
  libraries ship as packages.
- **Not the resolver.** The caller resolves a route (capability, offering,
  worker_url) before invoking the middleware.
- **Not the payment-daemon.** The caller mints the `Livepeer-Payment`
  envelope (base64 protobuf) before invoking the middleware.

## Wire-spec compliance

Implements the protocol at
[`../livepeer-network-protocol/`](../livepeer-network-protocol/):

- [`headers/livepeer-headers.md`](../livepeer-network-protocol/headers/livepeer-headers.md)
  defines what request headers we set and what response headers / trailers we
  read.
- Per-mode shapes:
  - [`modes/http-reqresp.md`](../livepeer-network-protocol/modes/http-reqresp.md)
    — implemented in `src/modes/http-reqresp.ts`.
  - [`modes/http-stream.md`](../livepeer-network-protocol/modes/http-stream.md)
    — implemented in `src/modes/http-stream.ts`. Uses Node's built-in
    `undici.request` for trailer access (the standard `fetch` API does not
    expose response trailers).
  - [`modes/http-multipart.md`](../livepeer-network-protocol/modes/http-multipart.md)
    — implemented in `src/modes/http-multipart.ts`.

## Internal architecture

See [`docs/design-docs/architecture.md`](./docs/design-docs/architecture.md)
for the planned package layout and request lifecycle.

## Dependencies

**Runtime: zero external deps.** Only Node built-ins (`fetch`, `undici`,
`node:http`).

**Dev only:** `typescript`, `@types/node`. Tests use `node:test`.
