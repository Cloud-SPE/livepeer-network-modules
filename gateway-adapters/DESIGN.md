# DESIGN — cross-language

Cross-language design notes for `gateway-adapters/`. Per-half design
detail lives at [`ts/DESIGN.md`](./ts/DESIGN.md) and
[`go/DESIGN.md`](./go/DESIGN.md). Cross-cutting design lives at the repo
root in [`../docs/design-docs/`](../docs/design-docs/).

## Why two halves

The HTTP-family adapters in
[`ts/src/modes/`](./ts/src/modes/) are TypeScript-only with near-zero
runtime dependencies. The non-HTTP modes do not all fit that mould:

- `ws-realtime@v0` — Node has the `ws` library and the OpenAI Realtime
  API path is already TS-on-the-gateway.
- `session-control-plus-media@v0` — splits along its own seam. The
  control-plane WebSocket is fine in TS, but the media plane (WebRTC
  SFU pass-through) needs `pion/webrtc` which is Go-native.
- `rtmp-ingress-hls-egress@v0` — the broker-side listener already uses
  `github.com/yutopp/go-rtmp`. A Go gateway-side adapter on the same
  library reuses ~80% of the implementor's mental model and exercises
  the same handshake edge cases on both sides of the wire.

So `gateway-adapters/` ships two halves: a TS package
(`@tztcloud/livepeer-gateway-middleware`) for HTTP and WebSocket
surfaces, and a Go module for RTMP and WebRTC. Each adopter writes in
their own language; no IPC tax between gateway code and adapter code.

## Wire-spec compliance

Both halves implement the protocol at
[`../livepeer-network-protocol/`](../livepeer-network-protocol/).
The five required `Livepeer-*` request headers are mirrored across both
halves (TS: `ts/src/headers.ts`; Go: `go/headers/headers.go`); change
the spec, change both.

## Payment integration

Both halves are agnostic to the payment-mint path:

- The gateway calls `PayerDaemon.CreatePayment` itself (over the
  payer-daemon's unix socket) before invoking the adapter.
- The adapter receives the base64-encoded payment envelope as an
  opaque string and attaches it to the `Livepeer-Payment` header (or
  the equivalent capability-defined slot for non-HTTP modes).

For long-lived sessions (`ws-realtime`,
`session-control-plus-media`), the broker accrues interim debits via
`PayeeDaemon.DebitBalance` calls (broker-side, see
[`../capability-broker/`](../capability-broker/)). On clean session
close the adapter consults the payer-daemon's session ledger via
`PayerDaemon.GetSessionDebits` to surface a final work-units count to
the gateway caller. Plan 0014 added the RPC surface; plan 0015 wires
the broker-side ticker that populates the ledger.

## Operator concerns

See [`docs/operator-runbook.md`](./docs/operator-runbook.md) for
per-mode ports, sizing, and NAT/firewall guidance for the WebRTC media
plane.
