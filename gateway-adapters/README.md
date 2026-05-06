# gateway-adapters

The TypeScript reference middleware for the gateway-side wire protocol per
[`../livepeer-network-protocol/`](../livepeer-network-protocol/). Distributed
as `@tztcloud/livepeer-gateway-middleware`.

> **For agents:** start at [`AGENTS.md`](./AGENTS.md).

## What it is

A small TypeScript library a gateway imports to talk to a livepeer
capability-broker. It owns:

- `Livepeer-*` request headers (Capability, Offering, Payment, Spec-Version,
  Mode, optional Request-Id) and response-header parsing.
- Per-mode middleware functions for the HTTP family
  (`http-reqresp@v0`, `http-stream@v0`, `http-multipart@v0`).
- A `LivepeerBrokerError` class that surfaces broker `Livepeer-Error` codes
  and `Livepeer-Backoff` advice.

It does **not** own:

- Customer-facing auth (gateway concern).
- Resolver integration with `service-registry-daemon` (gateway concern).
- `payment-daemon` (sender) integration; the gateway provides the
  base64-encoded payment envelope as a string.

## Status

**v0.1** — three HTTP-family modes implemented; no runtime dependencies; unit
tests via Node's built-in `node:test`. ws-realtime, rtmp-ingress-hls-egress,
and session-control-plus-media middleware are deferred to follow-up plans.

Tracked in [plan 0008](../docs/exec-plans/active/0008-gateway-adapters-typescript-middleware.md).

## Build + test

Per [core belief #15](../docs/design-docs/core-beliefs.md), every gesture is
Docker-first.

```bash
make build               # build the dev image
make test                # run unit tests in Docker
make help                # show all targets
```

No host `node` / `npm` install required.

## Layout

```
gateway-adapters/
├── package.json
├── tsconfig.json
├── Dockerfile / Makefile
├── src/
│   ├── headers.ts          # canonical Livepeer-* header constants + error codes
│   ├── errors.ts           # LivepeerBrokerError + parseError
│   ├── types.ts            # BrokerEndpoint + BrokerCall + helpers
│   ├── modes/
│   │   ├── http-reqresp.ts
│   │   ├── http-stream.ts
│   │   ├── http-multipart.ts
│   │   └── index.ts
│   └── index.ts            # public surface
├── test/
│   ├── http-reqresp.test.ts
│   ├── http-stream.test.ts
│   └── http-multipart.test.ts
└── docs/
```
