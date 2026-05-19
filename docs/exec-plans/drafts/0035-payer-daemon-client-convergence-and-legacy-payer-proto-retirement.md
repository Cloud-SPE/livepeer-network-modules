---
plan: 0035
title: Payer-daemon client convergence and legacy payer proto retirement
status: draft
phase: design
opened: 2026-05-19
owner: harness
related:
  - "completed plan 0025 — per-request ticket-params broker routing for resolver-selected payments"
  - "active plan 0034 — priced funding and final usage settlement"
  - "completed plan 0016 — chain-integrated payment-daemon — design choices"
---

# Plan 0035 — payer-daemon client convergence and legacy payer proto retirement

## 1. Problem

The active `payment-daemon` sender path already enforces the newer canonical
`CreatePayment` request contract from
`livepeer-network-protocol/proto/livepeer/payments/v1/payer_daemon.proto`:

- `recipient`
- `ticket_params_base_url`
- `accepted_price`
- `funding`

But the repo still contains older payer-daemon client shapes and a duplicate
legacy payer proto under `proto-contracts/`:

- some TypeScript gateway clients still construct requests using
  `face_value + capability + offering`
- `proto-contracts/livepeer/payments/v1/payer_daemon.proto` still defines the
  older payer request surface

That leaves the repo in an unstable state:

1. the active Go server expects the new request shape
2. multiple TS clients still model the old request shape
3. the duplicate legacy payer proto obscures the canonical contract

## 2. Goal

Converge every active payer-daemon client on one canonical request contract and
retire the duplicate legacy payer proto.

## 3. In scope

- identify every active `PayerDaemon.CreatePayment` client path
- migrate active TS callers to the canonical request contract
- choose one canonical proto source for payer-daemon client work
- delete the dead legacy payer proto and generated stubs once unused
- update tests to assert the canonical request fields
- document the canonical payer-daemon contract location

## 4. Out of scope

- retiring `proto-contracts` payee-daemon surfaces still used by Go callers
- redesigning pricing or settlement semantics
- changing receiver-mode payment validation
- reworking non-payer proto loading across the monorepo unless needed for this
  convergence

## 5. Current state

### 5.1 Canonical active server path

The Go `payment-daemon` server and related Go consumers already use:

- `livepeer-network-protocol/proto-go/livepeer/payments/v1`

Representative code:

- `payment-daemon/internal/server/server.go`
- `payment-daemon/internal/service/sender/sender.go`
- `livepeer-network-protocol/conformance/runner/internal/envelope/envelope.go`

### 5.2 Active or likely-active stale client paths

These need review and likely migration:

- `openai-gateway/src/livepeer/payment.ts`
- `daydream-gateway/src/paymentClient.ts`
- `vtuber-gateway/src/providers/payerDaemon.ts`
- `video-gateway/src/livepeer/payerDaemonClient.ts`

### 5.3 Legacy payer proto to retire

Candidate for retirement once no client depends on it:

- `proto-contracts/livepeer/payments/v1/payer_daemon.proto`
- generated files beside it

## 6. Execution

### 6.1 Confirm the canonical contract

Lock the canonical payer-daemon client contract to:

- `livepeer-network-protocol/proto/livepeer/payments/v1/payer_daemon.proto`

The canonical request must include:

- `recipient`
- `ticket_params_base_url`
- `accepted_price`
- `funding`

### 6.2 Migrate active clients

For each active TS client:

1. update request builders to the canonical fields
2. remove old face-value-only assumptions where they still drive request shape
3. align tests with canonical request capture

### 6.3 Retire the duplicate payer proto

Once no active caller depends on it:

1. delete `proto-contracts/livepeer/payments/v1/payer_daemon.proto`
2. delete the generated payer-daemon stubs beside it
3. leave payee-daemon and shared wire types alone unless separately proven dead

### 6.4 Tighten tests and docs

- add or update tests for the canonical request fields
- update docs that still describe the old payer request shape
- make the canonical proto location explicit in design docs and operator docs

## 7. Risks

- some TS clients may be inactive in production but still covered by tests or
  secondary binaries, so deletions need a full repo sweep
- `0034` settlement work overlaps this contract, so partial migration can leave
  the repo in a mixed state
- some dynamic TS gRPC loaders may need a compatibility decision:
  keep dynamic loading or move to generated client bindings

## 8. Exit criteria

This plan is complete when:

1. all active payer-daemon clients construct the canonical request shape
2. no active code depends on `proto-contracts/.../payer_daemon.proto`
3. the legacy payer proto and generated stubs are removed
4. tests assert the canonical request fields on the migrated clients
5. docs clearly point to one canonical payer-daemon client contract
