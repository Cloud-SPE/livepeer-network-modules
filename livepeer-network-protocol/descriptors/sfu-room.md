---
schema_name: sfu-room
tag: sfu-room/v1
version: 1.0.2-draft
status: draft
last_updated: 2026-08-19
---

# Descriptor schema: `sfu-room/v1`

A runner-hosted SFU conference room (LiveKit-shaped or equivalent): many
participants attach over WebRTC, media flows client↔SFU, and the gateway
admits participants by minting per-participant tokens against the runner.
This is the meeting-product schema; it satisfies requirements A1–A3 of the
2026-08-18 meeting handoff under the v1 protocols.

Typical offering axes: `attachment: external`, `metering: runner-reported`.
The **work unit is an offering property, not a schema property** — a room
capability may bill `participant_seconds`, `participant_minutes`, or any
other unit its operator declares. Nothing in this schema depends on the
choice; `price_per_unit_wei` and the runway fields simply scale with it.

## Public fields

| Field | Req | Customer-safe | Meaning |
|---|---|---|---|
| `url` | yes | yes | The SFU signaling endpoint participants connect to (`wss://…`). |
| `room` | yes | yes | Room identifier participants join. |
| `mint_url` | yes | no | Runner endpoint where the gateway presents the grant to mint participant tokens. Gateway-only. |
| `status_url` | no | no | Verifiability hook: room health/occupancy probe. Gateway-only; authorized by the same grant (see below). Independent of the broker's configured `session.runner.status_path` — different caller, different credential; a runner whose broker-facing status endpoint authenticates the broker's control token SHOULD expose a separate gateway-facing endpoint and publish that here. |

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
| `room-status` | absent (unbounded) | The same grant authorizes probes of `status_url`. |

Both operations ride **one** grant. The gateway is the only holder of the
grant secret, and `status_url` is the framework's verifiability hook — a
hook the gateway cannot authenticate against is not a hook, so the grant
that admits participants also authorizes reading the room's health. A
runner MUST accept the grant secret on both endpoints and MUST scope both
to this room.

Minting is deliberately repeated — every participant join is a mint — so
the grant is delivered once but usable until the session ends. Tokens
minted MUST be scoped to this room. Participant-token TTL SHOULD NOT
exceed **300 seconds**: a token is a join credential, revocation happens
by room removal rather than by expiry, so a short TTL bounds only the
join window and keeps a leaked token cheap.

## Metering

The offering declares `metering: runner-reported`, and that is the whole
story for billing the network: **the runner is the usage authority**,
measuring observed participant presence and reporting it as the session's
cumulative usage claim. Mint/TTL counting is not the meter and would
over-bill in two ordinary cases — a token minted for a participant who
never connects, and a participant who leaves before the token's TTL ends.

The gateway's own mint and attach records are still valuable, as the
**first-party cross-check** the dual-meter trust model calls for: they are
what the gateway compares runner claims against for divergence detection,
and what it may choose to bill *its own customers* from. That choice
belongs to the gateway; this schema does not dictate it.

## Conformance (public-by-contract)

`url`, `room`, `mint_url`, `status_url` — and nothing else — may appear in
this schema's public part. The schema's leak fixtures assert a token-mint
secret, an SFU API key, and a TURN credential each fail to surface in any
broker response.

## Changelog

| Version | Date | Change |
|---|---|---|
| 1.0.0-draft | 2026-08-18 | Initial schema. |
| 1.0.2-draft | 2026-08-19 | Meeting-team follow-up: `status_url` stated as independent of the broker's `session.runner.status_path` — two endpoints, two callers, two credentials. |
| 1.0.1-draft | 2026-08-19 | Meeting-team review: `room-status` added to the grant's operations so a gateway can actually authenticate the verifiability probe; metering section rewritten — the runner is the usage authority and mint records are a cross-check, not the billing basis; work unit stated as an offering property, not a schema property; participant-token TTL ceiling made explicit at 300s. |
