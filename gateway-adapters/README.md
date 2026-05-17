# gateway-adapters

Reference middleware for the gateway-side wire protocol per
[`../livepeer-network-protocol/`](../livepeer-network-protocol/).

> **For agents:** start at [`AGENTS.md`](./AGENTS.md).

## Two halves

`gateway-adapters/` is split into two language halves so each adopter
writes in their own language without an IPC tax:

| Half | Distribution | Modes covered |
|---|---|---|
| [`ts/`](./ts/) | npm package `@tztcloud/livepeer-gateway-middleware` | HTTP family (`http-reqresp@v0`, `http-stream@v0`, `http-multipart@v0`), `ws-realtime@v0`, control-WS surface of `session-control-plus-media@v0` |
| [`go/`](./go/) | Go module `github.com/Cloud-SPE/livepeer-network-modules/gateway-adapters/go` | `rtmp-ingress-hls-egress@v0` (RTMP listener + relay), WebRTC media-plane SFU pass-through for `session-control-plus-media@v0` |

The split mirrors production reality: the HTTP-family and WebSocket
adapters fit cleanly in Node-on-the-gateway deployments (the reference
[`openai-gateway/`](../openai-gateway/) is TypeScript). RTMP listeners
and WebRTC SFUs run in Go in production — the broker-side counterparts
at [`../capability-broker/internal/media/`](../capability-broker/internal/media/)
already use `yutopp/go-rtmp` and `pion/webrtc`, so the Go half reuses
the same libraries on the gateway side for identical handshake
behaviour and shared mental model.

## What it is

A small library a gateway imports to talk to a livepeer
capability-broker. Across both halves it owns:

- `Livepeer-*` request headers (Capability, Offering, Payment,
  Spec-Version, Mode, optional Request-Id) and response-header parsing.
- Per-mode middleware functions for each interaction mode in
  `../livepeer-network-protocol/modes/`.
- Structured broker errors (`Livepeer-Error` codes + `Livepeer-Backoff`
  advice).

It does **not** own:

- Customer-facing auth (gateway concern).
- Resolver integration with `service-registry-daemon` (gateway concern).
- The payment-mint path. The gateway calls
  `PayerDaemon.CreatePayment` itself and passes the resulting base64
  envelope to the adapter.

## Status

- **TS half (`ts/`)** — six modes implemented (HTTP family +
  `ws-realtime` + `session-control-plus-media` control-WS).
- **Go half (`go/`)** — RTMP listener + customer→broker relay +
  WebRTC SFU pass-through.

## Build + test

Per [core belief #15](../docs/design-docs/core-beliefs.md), every
gesture is Docker-first.

```bash
make -C ts test                # TS half (tsc + node:test in alpine image)
make -C go test                # Go half (go test -race -count=1 in golang image)
```

No host `node`, `npm`, or `go` install required.

## Layout

```
gateway-adapters/
├── AGENTS.md                  # cross-language agent map
├── CLAUDE.md
├── README.md                  # this file
├── DESIGN.md                  # cross-language design notes
├── docs/
│   └── operator-runbook.md    # per-mode ports, sizing, NAT/firewall
├── ts/                        # TypeScript half
│   ├── package.json           # @tztcloud/livepeer-gateway-middleware
│   ├── tsconfig.json
│   ├── Dockerfile / Makefile
│   ├── src/
│   │   ├── headers.ts         # canonical Livepeer-* header constants
│   │   ├── errors.ts
│   │   ├── types.ts
│   │   ├── modes/
│   │   │   ├── http-reqresp.ts
│   │   │   ├── http-stream.ts
│   │   │   ├── http-multipart.ts
│   │   │   ├── ws-realtime.ts
│   │   │   ├── session-control-plus-media.ts
│   │   │   └── index.ts
│   │   ├── payer-daemon.ts    # GetSessionDebits client
│   │   └── index.ts
│   └── test/
└── go/                        # Go half
    ├── go.mod                 # github.com/Cloud-SPE/.../gateway-adapters/go
    ├── Dockerfile / Makefile
    ├── headers/               # canonical Livepeer-* header constants (Go)
    ├── errors/
    ├── modes/
    │   ├── rtmpingresshlsegress/
    │   └── sessioncontrolplusmedia/
    └── internal/
```
