# Plan 0008 — gateway-adapters TypeScript reference middleware

**Status:** completed (2026-05-06)
**Opened:** 2026-05-06
**Closed:** 2026-05-06
**Owner:** initial author + assistant

## Goal

Stand up the `gateway-adapters/` component: a TypeScript library
implementing the gateway-side wire protocol for talking to a livepeer
capability-broker. Distributed as `@tztcloud/livepeer-gateway-middleware`.

## Why

The OpenAI-compat gateway (plan 0009 — first adopter) needs a Livepeer
client library. The reference middleware lives in this monorepo so the
spec, broker, and client all evolve together.

## Scope (v0.1 narrowed)

In scope:

- `gateway-adapters/` scaffold: package.json, tsconfig.json, Dockerfile,
  Makefile, src/ and test/ directories, component-local AGENTS.md +
  README.md + DESIGN.md.
- Per-mode middleware functions for the **HTTP family**:
  - `http-reqresp@v0` — built on Node's `fetch` (no trailer needs).
  - `http-stream@v0` — built on Node's bundled `undici.request` so
    response trailers are accessible (the standard `fetch` API does
    not expose trailers).
  - `http-multipart@v0` — built on `fetch`.
- Common types + headers + error class.
- Unit tests via Node's built-in `node:test`; mock broker in-process
  with `node:http` per test.
- TypeScript-only; zero runtime dependencies. Dev deps: `typescript` +
  `@types/node` only.

Out of scope (deferred):

- `ws-realtime@v0` — TypeScript WebSocket client; pick a library
  (`ws` or native) at follow-up time.
- `rtmp-ingress-hls-egress@v0`, `session-control-plus-media@v0` —
  session-open phase only is fine for v0.1; deferred.
- Resolver (`service-registry-daemon`) integration — gateway-side
  concern; not part of this middleware library.
- Payment-daemon (sender) integration — same.
- npm publish.

## Outcomes

- [x] `gateway-adapters/` scaffold landed.
- [x] `src/headers.ts`, `src/errors.ts`, `src/types.ts` exported.
- [x] `http-reqresp@v0` middleware function with unit tests passing.
- [x] `http-stream@v0` middleware function with unit tests passing
  (verifies trailer-based Livepeer-Work-Units reading via Node's
  built-in `node:http` module).
- [x] `http-multipart@v0` middleware function with unit tests passing.
- [x] `make test` builds + runs all unit tests in Docker.
- [x] Component-local docs match the broker's pattern.

## Done condition (met 2026-05-06)

```
$ make -C gateway-adapters test
...
# tests 10
# suites 3
# pass 10
# fail 0
```
