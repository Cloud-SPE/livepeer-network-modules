# Plan 0010 — ws-realtime@v0 driver pair

**Status:** completed (2026-05-06)
**Opened:** 2026-05-06
**Closed:** 2026-05-06
**Owner:** initial author + assistant

## Goal

Broker + runner drivers for `ws-realtime@v0`. WebSocket upgrade on
`GET /v1/cap`; bidirectional frame relay between gateway and backend;
Livepeer-* headers stripped on the outbound upgrade; backend auth injected
when configured.

## Scope (v0.1 narrowed)

In scope:

- Broker driver: WebSocket upgrade, dial backend WS (HTTP→WS URL
  conversion), bidirectional frame relay, log final byte counts on close.
- Runner driver: WebSocket dial against the broker, send a text frame,
  receive the echoed response (via the broker's relay), close cleanly,
  assert the broker's mock-backend received the upgrade with no
  Livepeer-* headers.
- Mock backend: extend with WS upgrade handler that records inbound headers
  and echoes any text frame.
- Fixture: `fixtures/ws-realtime/happy-path.yaml`.
- Test-config capability pointing at the runner's mock backend.

Out of scope (deferred):

- Interim debit at cadence (the spec's `cadence_seconds=5` pattern).
  v0.1 mock-payment middleware does a single Debit(estimate) up front
  and a Reconcile at session close; the cadence-debit mechanism is
  payment-daemon-integrated work (plan 0005).
- Balance-low signaling (`session.balance.low` control event).
- Idle / max-session timeouts beyond the WS library's defaults.

## Done condition

```
runner-1 |   PASS: happy-path [ws-realtime@v0]
```

`make test-compose` from `conformance/` exits 0 with all four fixtures
passing (http-reqresp, http-stream, http-multipart, ws-realtime).
