---
schema_name: trickle-egress
tag: trickle-egress/v1
version: 1.0.0-draft
status: draft
last_updated: 2026-08-18
---

# Descriptor schema: `trickle-egress/v1`

A runner-hosted generative session (avatar/vtuber-shaped) that renders A/V
and trickle-publishes it to a buyer-supplied egress destination, driven over
a runner-hosted control channel. The descriptor covers the runner's
coordinates; the egress destination travels the other direction.

Typical offering axes: `attachment: external`, `metering: runner-reported`,
work unit `session_seconds`.

## Direction of credentials — the asymmetry this schema makes explicit

The egress target (storage or relay endpoint plus its signed credentials) is
**buyer-supplied material**: it belongs in `session_params` at open, opaque
capability data the broker passes to the runner verbatim. It is NOT
descriptor content — descriptors describe the runner's runtime, never the
buyer's infrastructure. The legacy pattern of smuggling egress auth through
ad-hoc `params.extras` blobs becomes the *documented* home for it, while the
runner's own coordinates stop being invented per deployment.

## Public fields

| Field | Req | Customer-safe | Meaning |
|---|---|---|---|
| `control_url` | yes | no | Runner-hosted control-channel attach point (WS) for driving the session (chat, prompts, parameters). The gateway attaches and relays for its customers under its own auth. |
| `preview_url` | no | maybe | Optional preview/monitor stream. Customer-safety is offering-defined. |
| `status_url` | no | no | Verifiability hook: session/render health probe. |

## Private fields

None required.

## Grants

Exactly one:

| Operation | `max_uses` | Meaning |
|---|---|---|
| `control-attach` | absent (unbounded) | The grant secret authenticates the gateway's attach (and every re-attach after disconnect) to `control_url`, scoped to this session. |

Re-attach after a gateway restart is expected and costless: the resumable
session credential recovers the broker relationship, and this grant —
already in the gateway's hands — recovers the control channel. Neither
requires re-minting anything. The gateway's attach records and its own
customer bearers are its admission-edge meter of record.

## Conformance (public-by-contract)

`control_url`, `preview_url`, `status_url` — and nothing else — may appear
in this schema's public part. Leak fixtures assert the grant secret and any
`session_params` egress credentials never surface in any broker response.
