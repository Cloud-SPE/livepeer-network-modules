---
schema_name: sfu-room
tag: sfu-room/v1
version: 1.0.0-draft
status: draft
last_updated: 2026-08-18
---

# Descriptor schema: `sfu-room/v1`

A runner-hosted SFU conference room (LiveKit-shaped or equivalent): many
participants attach over WebRTC, media flows client↔SFU, and the gateway
admits participants by minting per-participant tokens against the runner.
This is the meeting-product schema; it satisfies requirements A1–A3 of the
2026-08-18 meeting handoff under the v1 protocols.

Typical offering axes: `attachment: external`, `metering: runner-reported`,
work unit `participant_minutes`.

## Public fields

| Field | Req | Customer-safe | Meaning |
|---|---|---|---|
| `url` | yes | yes | The SFU signaling endpoint participants connect to (`wss://…`). |
| `room` | yes | yes | Room identifier participants join. |
| `mint_url` | yes | no | Runner endpoint where the gateway presents the grant to mint participant tokens. Gateway-only. |
| `status_url` | no | no | Verifiability hook: room health/occupancy probe. |

## Private fields

None required. SFU API secrets, TURN shared secrets, and other long-lived
runner credentials are runner-internal and MUST NOT appear anywhere in the
descriptor — not in `private` either. `private` MAY carry a runner-issued
session-admin credential if the capability wants the broker able to perform
schema-specific teardown; v1 defines no such requirement.

## Grants

Exactly one:

| Operation | `max_uses` | Meaning |
|---|---|---|
| `participant-token-mint` | absent (unbounded) | The gateway presents the grant secret at `mint_url` to mint a short-lived participant token, per participant, for this room only. |

Minting is deliberately repeated — every participant join is a mint — so the
grant is delivered once but usable until the session ends. Each mint is the
gateway's admission-edge metering event: participant-minutes are computed
from the gateway's own mint/TTL records, per the dual-meter trust model.
Tokens minted MUST be scoped to this room and SHOULD have TTLs no longer
than the offering's heartbeat-detectable horizon.

## Conformance (public-by-contract)

`url`, `room`, `mint_url`, `status_url` — and nothing else — may appear in
this schema's public part. The schema's leak fixtures assert a token-mint
secret, an SFU API key, and a TURN credential each fail to surface in any
broker response.
