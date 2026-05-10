---
plan: 0025
title: Per-request ticket-params broker routing for resolver-selected payments
status: active
phase: implementation
opened: 2026-05-10
owner: harness
related:
  - "active plan 0024 — quote-free ticket-params flow across gateway, broker, and payment-daemon"
  - "completed plan 0013 — openai-gateway collapse"
  - "completed plan 0014 — wire-compat envelope + sender daemon"
  - "completed plan 0016 — chain-integrated payment-daemon — design choices"
---

# Plan 0025 — per-request ticket-params broker routing for resolver-selected payments

## 1. Problem

The quote-free sender flow is broken when the sender fetches ticket
params from any broker other than the one the gateway selected:

- `openai-gateway` resolves a concrete broker candidate per request.
- `payment-daemon` sender mode must fetch ticket params from that exact
  selected broker origin.

That is correct for a single-broker deployment, but wrong for the
resolver-driven architecture:

1. gateway can select broker A for the request,
2. sender daemon can fetch ticket params from broker B,
3. gateway can then submit the signed payment to broker A.

Tickets must be minted against the exact payee/broker that will validate
them. A global sender-side ticket-params URL breaks that invariant.

## 2. Required invariant

For every paid request:

1. the gateway selects a specific broker candidate,
2. the payer daemon fetches ticket params from that exact broker,
3. the payer daemon signs against those payee-issued params,
4. the gateway sends the payment to that same broker.

The route selector chooses the broker. The payer daemon must not
re-resolve the payee independently.

## 3. Execution

### 3.1 Extend the payer-daemon proto

Add an optional field to `CreatePaymentRequest`:

- `ticket_params_base_url`

This is the broker origin the sender should use for:

- `POST /v1/payment/ticket-params`

The field is required in practice:

- gateway-backed flows pass it per request,
- sender mode rejects payment creation when it is absent.

### 3.2 Thread the selected broker URL through the gateway

The route selector already returns:

- `brokerUrl`
- `offering`
- `ethAddress`

The dispatch layer must pass `candidate.brokerUrl` into
`buildPayment(...)`, and `buildPayment(...)` must include it in the
`CreatePayment` gRPC request.

This keeps:

- route selection
- ticket-param source
- final dispatch target

on the same broker candidate.

### 3.3 Make sender mode honor per-request base URLs

`payment-daemon` sender mode should:

- require `CreatePaymentRequest.ticket_params_base_url`,
- reject `CreatePayment` when it is absent.

The fetcher interface should take the base URL as part of the fetch
request so sender state is not tied to one global origin.

### 3.4 Remove global sender overrides

Do not keep a daemon-level ticket-params base URL flag. A global sender
override is incompatible with resolver-selected broker routing and would
silently reintroduce payee mismatches.

## 4. Files to change

### `livepeer-network-protocol/`

- `proto/livepeer/payments/v1/payer_daemon.proto`
- regenerate committed Go bindings under `proto-go/livepeer/payments/v1/`

### `openai-gateway/`

- `src/livepeer/payment.ts`
- `src/service/routeDispatch.ts`
- payment-related tests

### `payment-daemon/`

- `internal/service/sender/sender.go`
- `internal/service/sender/ticketparams_fetcher.go`
- `cmd/livepeer-payment-daemon/main.go`
- sender tests

## 5. Non-goals

- changing how the broker proxies `GetTicketParams`
- making the payer daemon query the service registry directly
- redesigning route selection policy
- auth-gating the broker ticket-params endpoint

This plan only aligns payment minting with the already-selected broker.
