# DESIGN — Go half

Component-local design summary for the Go half of `gateway-adapters/`.
The TS half lives at [`../ts/`](../ts/); cross-language design notes
live at [`../DESIGN.md`](../DESIGN.md). Cross-cutting design lives at
the repo root in [`../../docs/design-docs/`](../../docs/design-docs/).

## What this half is

A Go module a Go gateway imports to:

1. Accept RTMP pushes from a customer's encoder, look up the
   associated session, and relay frames to the broker's
   `rtmp_ingest_url` (`rtmp-ingress-hls-egress@v0`).
2. Mediate SDP exchange between a customer's WebRTC peer and the
   broker's media plane as a SFU pass-through, with no transcoding
   (`session-control-plus-media@v0` media surface).

## What it is not

- **Not a runtime service.** It's a library imported into a gateway
  service. Per core belief #15, services ship as Docker images;
  libraries ship as packages. (The `Dockerfile` here builds the test
  image only.)
- **Not a transcoder.** No FFmpeg, no media inspection. Frames pass
  through untouched.
- **Not the session-open path.** Session-open for non-HTTP modes is
  HTTP-shaped and lives in the TS half (`../ts/src/modes/`). Go
  gateways that need session-open call the broker over plain HTTP
  themselves.

## Wire-spec compliance

Implements the protocol at
[`../../livepeer-network-protocol/`](../../livepeer-network-protocol/):

- [`headers/livepeer-headers.md`](../../livepeer-network-protocol/headers/livepeer-headers.md)
  — mirrored at `headers/headers.go`.
- Per-mode shapes:
  - [`modes/rtmp-ingress-hls-egress.md`](../../livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md)
    — implemented in `modes/rtmpingresshlsegress/`.
  - [`modes/session-control-plus-media.md`](../../livepeer-network-protocol/modes/session-control-plus-media.md)
    — media-plane SFU implemented in `modes/sessioncontrolplusmedia/`.
    (Control-WS lives in the TS half.)

## Library pins

- `github.com/yutopp/go-rtmp` — RTMP listener. Pure-Go, MIT-licensed,
  suite-validated. Same pin as the broker-side listener at
  `../../capability-broker/internal/media/rtmp/`. Identical handshake
  behaviour on both sides of the wire ensures customer encoders that
  pass against the broker also pass against the gateway.
- `github.com/pion/webrtc/v3` — WebRTC SFU pass-through. The only
  production-quality option for Go-side WebRTC. Same pin the
  broker-side media plane uses.

## Dependencies

`yutopp/go-rtmp`, `pion/webrtc/v3`, and `google.golang.org/grpc` (for
calling `PayerDaemon.GetSessionDebits` over the unix socket on session
close). The Go-grpc dependency is shared with payer-daemon clients
elsewhere in the monorepo.
