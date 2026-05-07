# AGENTS.md

This is `gateway-adapters/` — reference middleware for the gateway-side
wire protocol per
[`../livepeer-network-protocol/`](../livepeer-network-protocol/).

The component is split into two language halves so each adopter writes
in their own language; no IPC tax:

- [`ts/`](./ts/) — TypeScript package
  `@tztcloud/livepeer-gateway-middleware`. Hosts the HTTP family
  (`http-reqresp@v0`, `http-stream@v0`, `http-multipart@v0`),
  `ws-realtime@v0`, and the control-WS surface of
  `session-control-plus-media@v0`.
- [`go/`](./go/) — Go module
  `github.com/Cloud-SPE/livepeer-network-rewrite/gateway-adapters/go`.
  Hosts `rtmp-ingress-hls-egress@v0` (RTMP listener +
  customer→broker relay) and the WebRTC media-plane SFU pass-through
  for `session-control-plus-media@v0`.

Component-local agent map. The repo-root [`../AGENTS.md`](../AGENTS.md) is
the cross-cutting map; this file scopes to gateway-adapters-specific
guidance and points at each half.

## When to use which half

| You're writing... | Use this half |
|---|---|
| TS gateway, HTTP-family capability | [`ts/`](./ts/) — import the per-mode subpath. |
| TS gateway, OpenAI Realtime API or any long-lived bidirectional WS | [`ts/`](./ts/) — `./modes/ws-realtime`. |
| TS gateway, vtuber-style control-plane (signalling on WS, media on WebRTC) | [`ts/`](./ts/) for the control WS, [`go/`](./go/) sidecar for the media plane. |
| Go gateway, RTMP ingest | [`go/`](./go/) — `modes/rtmpingresshlsegress`. |
| Go gateway, WebRTC SFU pass-through | [`go/`](./go/) — `modes/sessioncontrolplusmedia`. |

## Operating principles

Inherited from the repo root. Plus:

- **Library code, not a service.** The gateway operator imports each
  half and wires it into their request pipeline. The TS half ships as
  npm; the Go half ships as an importable Go module. Each component's
  `Dockerfile` is for the test/build environment, not for production
  deployment (per core belief #15 — services ship as images, libraries
  ship as packages).
- **Minimal runtime dependencies.** TS half: `ws` and `@grpc/grpc-js`
  only (the HTTP modes stay zero-dep). Go half: `github.com/yutopp/go-rtmp`
  (RTMP listener — pinned to align with the broker-side library at
  `../capability-broker/internal/media/rtmp/`) and
  `github.com/pion/webrtc/v3` (WebRTC SFU pass-through). Adding any
  other runtime dependency requires a planned exec-plan.
- **The spec is the source of truth.** When code disagrees with
  `../livepeer-network-protocol/`, the spec wins.

## Where to look

| Question | File |
|---|---|
| What is this component? | [`README.md`](./README.md) |
| Cross-language design | [`DESIGN.md`](./DESIGN.md) |
| TS half agent map | [`ts/AGENTS.md`](./ts/AGENTS.md) |
| Go half agent map | [`go/AGENTS.md`](./go/AGENTS.md) |
| Operator-facing runbook | [`docs/operator-runbook.md`](./docs/operator-runbook.md) |
| The wire spec this implements | [`../livepeer-network-protocol/`](../livepeer-network-protocol/) |
| The broker it talks to | [`../capability-broker/`](../capability-broker/) |

## Doing work in this component

- **All gestures are Docker-first** (per repo-root core belief #15). Use
  `make build` / `make test` from each half. No host npm or go install
  required at usage time.
- **TS half** is `tsc`-strict, tests use `node:test`. See
  [`ts/AGENTS.md`](./ts/AGENTS.md).
- **Go half** is `go test -race -count=1 ./...` from `go/`. See
  [`go/AGENTS.md`](./go/AGENTS.md).
- **Dependencies stay current** (per repo-root core belief #16).

## What lives elsewhere

- The wire spec → `../livepeer-network-protocol/`.
- The broker reference impl → `../capability-broker/`.
- Cross-cutting design → `../docs/design-docs/`.
- Repo-wide exec plans → `../docs/exec-plans/active/`.
