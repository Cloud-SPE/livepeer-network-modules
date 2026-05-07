# AGENTS.md — TS half

This is the TypeScript half of `gateway-adapters/`. Distributed as
`@tztcloud/livepeer-gateway-middleware`. The Go half lives at
[`../go/`](../go/); the cross-language map lives at
[`../AGENTS.md`](../AGENTS.md).

## Operating principles

Inherited from the repo root and the parent `gateway-adapters/AGENTS.md`.
Plus:

- **Library code, not a service.** Imported by the gateway operator into
  their request pipeline. Ships as npm. The Dockerfile is for the
  test/build environment, not for production deployment (per core
  belief #15).
- **Minimal runtime dependencies.** HTTP-family modes use only Node
  built-ins. The `ws-realtime` and `session-control-plus-media` modes
  depend on `ws` (Node has no built-in WebSocket client) and
  `@grpc/grpc-js` (only for `PayerDaemon.GetSessionDebits` lookup
  on session close). Adding any other runtime dependency requires a
  planned exec-plan.
- **The spec is the source of truth.** When code disagrees with
  `../../livepeer-network-protocol/`, the spec wins.

## Where to look

| Question | File |
|---|---|
| What is this half? | [`../README.md`](../README.md) |
| TS-half design | [`DESIGN.md`](./DESIGN.md) |
| Build / test gestures | [`Makefile`](./Makefile) |
| The wire spec this implements | [`../../livepeer-network-protocol/`](../../livepeer-network-protocol/) |
| The broker it talks to | [`../../capability-broker/`](../../capability-broker/) |

## Doing work in this half

- **All gestures are Docker-first.** Use `make build`, `make test`. No
  host npm install required.
- **TypeScript with strict types.** `tsc` is the source of truth for
  type-checking; `tsconfig.json` enables `strict` mode.
- **Tests use `node:test`** — no jest, no vitest, no mocha.
- **Dependencies stay current** (per repo-root core belief #16).

## What lives elsewhere

- Go half (RTMP listener, WebRTC SFU pass-through) → [`../go/`](../go/).
- The wire spec → `../../livepeer-network-protocol/`.
- The broker reference impl → `../../capability-broker/`.
- Cross-cutting design → `../../docs/design-docs/`.
- Repo-wide exec plans → `../../docs/exec-plans/active/`.
