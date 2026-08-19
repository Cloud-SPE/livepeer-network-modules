# Descriptor schemas

Runtime-descriptor schemas for `paid-session/v1` offerings. The framework —
envelope, public/private partition, grants, validation — is defined in
[`../protocols/runtime-descriptor.md`](../protocols/runtime-descriptor.md);
each document here defines one schema's fields, grants, and
public-by-contract conformance surface.

Runner authors: the obligations a schema places on you sit alongside the
protocol's in
[`paid-session/v1` §11](../protocols/paid-session.md#11-runner-obligations--the-implementers-checklist).

A schema is owned by its capability. Adding one requires changes only in the
runner that emits it and the gateway that consumes it — no broker,
clearinghouse, or registry work.

| Schema | Workload shape | Grant |
|---|---|---|
| [`sfu-room/v1`](./sfu-room.md) | SFU conference room, WebRTC participants | `participant-token-mint` |
| [`rtmp-hls/v1`](./rtmp-hls.md) | RTMP ingest → HLS playback | `stream-key-issue` |
| [`scope-passthrough/v1`](./scope-passthrough.md) | Interactive HTTP+WebRTC API surface | `scope-api-access` |
| [`trickle-egress/v1`](./trickle-egress.md) | Generative A/V session, buyer-supplied egress | `control-attach` |

Conventions the four v1 schemas established (follow unless a workload truly
differs): exactly one grant per schema; `max_uses` absent (admission
operations repeat — every join, key rotation, or re-attach is a use); grants
die with the session regardless of `expires_at`; the verifiability hook is a
`status_url` (or an inherently probeable coordinate like an HLS playlist);
buyer-supplied credentials ride in `session_params`, never in the
descriptor.
