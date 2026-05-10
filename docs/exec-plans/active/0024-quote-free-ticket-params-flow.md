---
plan: 0024
title: Quote-free ticket-params flow across gateway, broker, and payment-daemon
status: active
phase: implementation
opened: 2026-05-10
owner: harness
related:
  - "completed plan 0005 — payment-daemon component"
  - "completed plan 0014 — wire-compat envelope + sender daemon"
  - "completed plan 0016 — chain-integrated payment-daemon — design choices"
  - "completed plan 0013 — openai-gateway collapse"
---

# Plan 0024 — quote-free ticket-params flow across gateway, broker, and payment-daemon

## 1. Problem

The current rewrite payment flow is cryptographically inconsistent on the sender path:

- `openai-gateway` calls `PayerDaemon.CreatePayment(...)`.
- `payment-daemon` sender mode still fabricates `TicketParams` locally.
- `payment-daemon` receiver mode validates `recipientRand` against the payee-issued `recipientRandHash`.

That mismatch produces receiver-side rejections like:

- `invalid recipient`
- `invalid recipientRand for recipientRandHash`

There is a second mismatch in the broker:

- the broker currently opens payee sessions on `Livepeer-Request-Id`
- the quote-free ticket flow is naturally keyed by `ticket_params.recipient_rand_hash`

Even after the sender fetches canonical ticket params, validation still fails unless the broker uses the same payee work id the receiver already associated with those ticket params.

## 2. Required invariant

For the quote-free path, these three facts must all be true:

1. The payee owns `recipientRandHash`.
2. The sender signs against the payee-issued `TicketParams`.
3. The broker uses the same `work_id` when it calls:
   - `OpenSession`
   - `ProcessPayment`
   - `DebitBalance`
   - `CloseSession`

In this rewrite, the correct work id is:

- `hex(ticket_params.recipient_rand_hash)`

That matches the receiver-side session opened during `GetTicketParams(...)`.

## 3. Execution

### 3.1 Broker-side ticket-params proxy

Add an unpaid HTTP endpoint on `capability-broker`:

- `POST /v1/payment/ticket-params`

It proxies to the local receiver-mode `PayeeDaemon.GetTicketParams(...)` over the existing unix-socket gRPC client.

Request body:

- `sender_eth_address`
- `recipient_eth_address`
- `face_value_wei`
- `capability`
- `offering`

Response body:

- `ticket_params`

This mirrors the older worker-side shape so sender-mode callers can fetch canonical payee-issued ticket params without talking to the payee daemon directly.

### 3.2 Sender-mode ticket-params fetch

Replace the sender stub path with a real fetcher:

- `CreatePayment(...)` receives the broker-origin URL from the caller
- sender mode POSTs to `${baseURL}/v1/payment/ticket-params`
- it receives canonical `TicketParams`
- it signs against those params, not against a locally fabricated copy

For the payment blob:

- `TicketParams.face_value` comes from the payee response
- `expected_value` is computed from the payee response

The caller-provided `face_value` remains the target spend input to the fetch request, but not the source of truth for the signed ticket fields.

### 3.3 Broker payee work-id derivation

Change the broker payment middleware so the payee-side `work_id` is derived from the inbound payment bytes:

- unmarshal `livepeer.payments.v1.Payment`
- read `payment.ticket_params.recipient_rand_hash`
- hex-encode that hash

That derived id becomes the broker's internal session key for:

- `OpenSession`
- `ProcessPayment`
- interim `DebitBalance`
- final `DebitBalance`
- `CloseSession`

`Livepeer-Request-Id` remains a tracing/request-correlation header. It is no longer the payee-side session key on the quote-free payment path.

Fallback behavior for legacy tests/dev stubs:

- if the broker cannot derive a recipient-rand-hash-backed work id from the payment bytes, it falls back to the request id

That preserves existing mock-based middleware tests while making the real path cryptographically correct.

## 4. Files to change

### `capability-broker/`

- extend `internal/payment/client.go`
- extend `internal/payment/grpc.go`
- extend `internal/payment/mock.go`
- add ticket-params HTTP handler under `internal/server/`
- register `POST /v1/payment/ticket-params`
- add payee work-id derivation helper in the payment middleware
- update/add broker tests

### `payment-daemon/`

- add sender HTTP ticket-params fetcher
- change `internal/service/sender/sender.go`
- wire the per-request broker URL into sender-mode `CreatePayment(...)`
- update sender tests

## 5. Non-goals in this plan

- service-registry-daemon-driven recipient→URL resolution inside sender mode
- bearer-auth protecting the broker ticket-params endpoint
- redesigning receiver `GetTicketParams(...)` idempotency semantics
- changing the `PayerDaemon` proto

Those can follow once the rewrite's quote-free sender/payee path is cryptographically sound.
