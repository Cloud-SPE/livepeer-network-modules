---
schema_name: scope-passthrough
tag: scope-passthrough/v1
version: 1.0.0-draft
status: draft
last_updated: 2026-08-18
---

# Descriptor schema: `scope-passthrough/v1`

A runner-hosted interactive HTTP+WebRTC API surface (Daydream-Scope-shaped):
the gateway proxies or points clients at a session-scoped API base; media is
negotiated directly client↔runtime over WebRTC. Successor to the gen-1
`mediaDescriptor{schema, scope_url}` shape, with the access-control hole
closed: the scope surface is no longer an unauthenticated URL.

Typical offering axes: `attachment: external`, `metering: runner-reported`,
work unit `session_seconds`.

## Public fields

| Field | Req | Customer-safe | Meaning |
|---|---|---|---|
| `scope_url` | yes | no | Session-scoped API base for the runtime's own surface (signaling, prompts, runtime params). The gateway proxies it or brokers access; it is not handed raw to customers. |
| `status_url` | no | no | Verifiability hook: runtime health probe. |

## Private fields

None required.

## Grants

Exactly one:

| Operation | `max_uses` | Meaning |
|---|---|---|
| `scope-api-access` | absent (unbounded) | The grant secret is the bearer credential for requests to `scope_url`, scoped to this session. |

The gen-1 shape's known weakness — `scope_url` accepted unauthenticated
requests from anyone holding the URL — is closed by making API access itself
the granted operation. Every proxied call the gateway makes carries the
grant secret; the gateway's proxy log of session-attached time is its
admission-edge meter of record.

## Conformance (public-by-contract)

`scope_url`, `status_url` — and nothing else — may appear in this schema's
public part. Leak fixtures assert the grant secret never surfaces in any
broker response.
