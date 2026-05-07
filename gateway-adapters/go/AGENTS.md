# AGENTS.md — Go half

This is the Go half of `gateway-adapters/`. Distributed as the Go module
`github.com/Cloud-SPE/livepeer-network-rewrite/gateway-adapters/go`.
The TS half lives at [`../ts/`](../ts/); the cross-language map lives at
[`../AGENTS.md`](../AGENTS.md).

## Operating principles

Inherited from the repo root and the parent `gateway-adapters/AGENTS.md`.
Plus:

- **Library code, not a service.** Imported by Go gateways into their
  request pipeline. Ships as a Go module. The Dockerfile is for the
  test/build environment, not for production deployment (per core
  belief #15).
- **Pinned RTMP and WebRTC libraries.**
  - `github.com/yutopp/go-rtmp` — RTMP listener. Same library as the
    broker-side listener at
    `../../capability-broker/internal/media/rtmp/`. Identical
    handshake handling on both sides of the wire.
  - `github.com/pion/webrtc/v3` — WebRTC SFU pass-through. Same
    library the broker-side media plane uses; the only
    production-quality option in Go.
- **The spec is the source of truth.** When code disagrees with
  `../../livepeer-network-protocol/`, the spec wins.

## Where to look

| Question | File |
|---|---|
| What is this half? | [`../README.md`](../README.md) |
| Go-half design | [`DESIGN.md`](./DESIGN.md) |
| Build / test gestures | [`Makefile`](./Makefile) |
| The wire spec this implements | [`../../livepeer-network-protocol/`](../../livepeer-network-protocol/) |
| The broker it talks to | [`../../capability-broker/`](../../capability-broker/) |

## Doing work in this half

- **All gestures are Docker-first.** Use `make build`, `make test`. No
  host go install required.
- **Tests:** `go test -race -count=1 ./...` — same as
  capability-broker.
- **Module path** is `github.com/Cloud-SPE/livepeer-network-rewrite/gateway-adapters/go`.
  The trailing `/go` is the literal path segment; sub-module of the
  monorepo, mirroring the `payment-daemon/`, `capability-broker/`,
  etc. precedents.
- **Dependencies stay current** (per repo-root core belief #16).

## What lives elsewhere

- TS half (HTTP family, ws-realtime, control-WS) → [`../ts/`](../ts/).
- The wire spec → `../../livepeer-network-protocol/`.
- The broker reference impl → `../../capability-broker/`.
- Cross-cutting design → `../../docs/design-docs/`.
- Repo-wide exec plans → `../../docs/exec-plans/active/`.
