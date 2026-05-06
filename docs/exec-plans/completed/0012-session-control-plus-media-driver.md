# Plan 0012 — session-control-plus-media@v0 driver pair (session-open phase)

**Status:** completed (2026-05-06)
**Opened:** 2026-05-06
**Closed:** 2026-05-06
**Owner:** initial author + assistant

## Goal

Broker + runner drivers for the **session-open phase** of
`session-control-plus-media@v0`. The broker accepts the session-open POST
and returns 202 with `session_id`, `control_url` (WebSocket), an opaque
capability-defined `media` descriptor, and `expires_at`. The runner
asserts the wire shape.

## Scope (deliberately narrow, parallel to plan 0011)

In scope:

- Broker driver: handle `POST /v1/cap` for `session-control-plus-media@v0`.
  Generate `session_id`, return 202 with the required body fields.
  `media.publish_url` and `media.publish_auth` are placeholder values
  derived from `Capability.Backend.URL`; the broker does not stand up the
  media plane in v0.1.
- Runner driver: send the session-open POST, assert 202 + body fields
  present.
- Fixture: `fixtures/session-control-plus-media/happy-path.yaml`.
- Test-config capability declaration.

Out of scope (deferred):

- Control-plane WebSocket lifecycle (open / event relay / close). The
  WebSocket transport pattern lands in plan 0010; reusing it for the
  control plane in this mode is a follow-up.
- Capability-defined media plane provisioning (pytrickle URL minting,
  per-session secrets, backend session-runner startup).
- Cadence-based debit during the session.
- Control-WS-not-opened-within-expires_at auto-close + refund.
- Control-event semantics (`session.balance.low`, `session.usage.tick`,
  `session.error`, `session.ended`).

## Done condition

`make test-compose` from `conformance/` exits 0 with the v0.1 fixture set
+ session-control-plus-media/happy-path.yaml.
