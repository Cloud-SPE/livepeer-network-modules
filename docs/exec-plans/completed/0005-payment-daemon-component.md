# Plan 0005 — payment-daemon component (Option 2)

**Status:** active
**Opened:** 2026-05-06
**Owner:** harness
**Related:** PLANS.md "Phase 4 — Real payment-daemon integration"

## Problem

Until now the broker has used an in-process `payment.Mock` for the
session lifecycle (OpenSession / Debit / Reconcile / Close). Two things
are missing:

1. There is no out-of-process daemon for the broker to talk to, so the
   payment surface has zero deployment realism. In production the
   payment-daemon will be a long-lived sidecar holding the warm key,
   not an embedded library.
2. The `Livepeer-Payment` HTTP-header value is currently any
   non-empty string. The spec says it MUST be a base64-encoded
   protobuf `Payment` message carrying `capability_id`,
   `offering_id`, and `expected_max_units`. The broker is not yet
   decoding it, so the cross-check between header values and envelope
   contents is not enforced and the per-request debit estimate is
   hard-coded to `1`.

This plan addresses both — without yet involving any chain.

## Scope

### In scope (v0.1)

- Add the canonical wire definition for the Livepeer-Payment envelope
  and the receiver-side gRPC service to
  `livepeer-network-protocol/proto/livepeer/payments/v1/`.
- Stand up a new `payment-daemon/` component as a Go binary that
  serves the `livepeer.payments.v1.PayeeDaemon` gRPC service over a
  unix socket and persists session state in BoltDB
  (`go.etcd.io/bbolt`).
- Wire the broker's `payment.Client` interface to a real gRPC client
  alongside the existing `Mock` (selected by host-config). Make it the
  default in compose stacks.
- Decode the inbound `Livepeer-Payment` header in the broker and
  cross-check `capability_id` / `offering_id` against the
  `Livepeer-Capability` / `Livepeer-Offering` headers; reject on
  mismatch with `payment_envelope_mismatch`.
- Replace the hard-coded estimate with `expected_max_units` from the
  decoded envelope.
- Update the conformance runner and the OpenAI-compat gateway to mint
  real envelopes (not the literal string `"stub-payment"`).
- Update both compose stacks (`livepeer-network-protocol/conformance`
  and `openai-gateway`) to add `payment-daemon` as a sidecar with a
  shared unix-socket volume.
- All 11 conformance fixtures and all 10 openai-gateway smoke
  assertions must continue to pass against the real daemon.

### Out of scope (deferred)

- **Chain integration.** The receiver accepts any non-empty `ticket`
  bytes and records them; no probabilistic-micropayment validation,
  no Arbitrum, no signature verification, no redemption. Tracked as
  separate workstream "real-chain payment-daemon".
- **Sender-mode RPCs.** Both the runner and the gateway encode
  envelopes locally (Go using the generated bindings, TS via a small
  hand-rolled encoder). Standing the sender side up as its own gRPC
  surface is a follow-up; the wire format is locked in this plan, so
  that follow-up is mechanical.
- **Interim-debit cadence for long-running modes.** ws-realtime,
  rtmp-ingress-hls-egress, and session-control-plus-media still emit a
  single Debit(estimate) + Reconcile(actual) at session close. A
  ticker-driven interim-debit loop is its own plan; the gRPC surface
  in this plan supports multiple `Debit` calls per session, so that
  follow-up is mechanical.
- **Warm-key lifecycle.** The daemon stores no key material in v0.1.

## Commit cadence

Three reviewable commits land plan 0005:

1. **`feat(spec+payment-daemon): proto + standalone gRPC daemon`**
   - Spec: `livepeer-network-protocol/proto/livepeer/payments/v1/`
     defines `Payment` and the `PayeeDaemon` service. Generated Go
     bindings live next to the daemon (`payment-daemon/internal/proto/`).
   - Component: `payment-daemon/` with a Docker-first build, unix-socket
     listener, BoltDB persistence, integration-style self-test.
   - **Broker is unchanged.** This commit can ship in isolation.

2. **`feat(broker+runner): real payment-daemon integration`**
   - Broker grows a `payment.GRPC` client and decodes/verifies the
     `Livepeer-Payment` envelope; mock stays in-tree for unit tests.
   - Conformance runner mints real envelopes via the generated Go
     bindings; `livepeer-network-protocol/conformance/compose.yaml`
     adds a `payment-daemon` sidecar.
   - All 11 conformance fixtures still pass.

3. **`feat(openai-gateway): real payment-daemon integration`**
   - Hand-rolled TS protobuf encoder in `openai-gateway/src/livepeer/`
     (zero deps; mirrors the four-field `Payment` message).
   - `openai-gateway/compose.yaml` adds the same sidecar pattern.
   - All 10 smoke assertions still pass.

## Acceptance

- `make smoke` in `livepeer-network-protocol/conformance/` and
  `openai-gateway/` both pass with the real daemon in front of the
  broker.
- The `Livepeer-Payment` header on every paid request decodes to a
  protobuf `Payment` message whose `capability_id` / `offering_id`
  match the request headers.
- The broker's `Debit(estimate)` call uses the envelope's
  `expected_max_units`, not a constant.

## Tech-debt opened by this plan

- Inlined TS protobuf encoder in `openai-gateway/`. A real client
  would import the generated bindings; we hand-roll for v0.1 to keep
  zero runtime deps. Tracked in `openai-gateway/docs/exec-plans/tech-debt-tracker.md`.
- Sender-mode gRPC service surface deferred (see "Out of scope").
- Interim-debit cadence deferred.
- Chain integration deferred.
