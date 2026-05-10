---
plan: 0001-usage-reconciliation-and-mode-aware-chat
title: usage reconciliation and mode-aware chat selection
status: active
phase: phase-1a
opened: 2026-05-09
owner: harness
audience: openai-gateway maintainers
related:
  - "completed plan 0013-openai-gateway-collapse"
  - "customer-portal ledger and portal surfaces"
  - "capability-broker interaction-mode contract"
---

# Plan 0001 - usage reconciliation and mode-aware chat selection

## 1. Why this plan exists

The current gateway can route requests to capability-brokers and return
OpenAI-shaped responses, but two operator-visible gaps remain:

1. Chat mode selection is incomplete.
   Customers need to choose streaming or non-streaming chat explicitly,
   and route selection must honor that choice.
2. Customer-visible billing reconciliation is incomplete.
   The customer and admin portals are built around a reservation ledger,
   but the gateway does not yet fully persist request settlement from
   returned work-unit data.

This plan closes those gaps in phases so customer-visible correctness
lands before deeper accounting refinements.

## 2. End state

After this plan:

- The customer playground exposes a stream/non-stream control for chat.
- The gateway forwards chat requests only to routes whose
  `interaction_mode` matches the requested mode.
- Streaming chat requests force upstream usage emission so settlement can
  use actual work units.
- The reservation ledger records reserved, committed, refunded, and
  resolved values per request.
- The customer portal and admin portal show reconciled usage and payment
  state from the same durable source of truth.

## 3. Phases

### Phase 1A - mode-aware selection and playground support

Goals:

- Add a stream toggle to the customer playground.
- Carry `stream: true|false` through the chat request path.
- Make route selection mode-aware.
- Surface per-model supported chat modes in the playground catalog.

Changes:

- Extend route candidates with `interactionMode`.
- Filter resolver-backed route selection on requested mode.
- Aggregate model-catalog entries with `supported_modes`.
- Add chat stream toggle in the customer playground.
- For streaming playground requests, preserve customer auth headers and
  display the raw SSE response body.
- Force `stream_options.include_usage=true` upstream for streaming chat
  while keeping customer-visible behavior OpenAI-compatible.

Exit criteria:

- Non-stream chat selects only `http-reqresp@v0` routes.
- Stream chat selects only `http-stream@v0` routes.
- Unsupported model/mode combinations fail cleanly before dispatch.

### Phase 1B - reservation settlement for chat

Goals:

- Persist reservation lifecycle changes for chat completions.
- Reconcile reserved vs committed vs refunded values using actual work
  units and the gateway rate card.

Changes:

- Add reservation repo/service helpers for create, commit, refund, and
  partial settlement.
- Use `Livepeer-Work-Units` from broker responses for req/resp and
  stream completion.
- Mark settled rows with `resolved_at` and explicit state.
- Record enough metadata for portal/admin drilldown.

Exit criteria:

- Successful chat requests update committed tokens and committed cost.
- Failed pre-output requests refund fully.
- Partial-stream failures settle as partial instead of staying reserved.

### Phase 2 - portal and admin reconciliation visibility

Goals:

- Make the customer portal and admin console trustworthy views of
  request settlement.

Changes:

- Tighten customer usage responses to emphasize resolved usage.
- Add/finish admin reservation APIs and detail views.
- Show mode, reserved, committed, refunded, and resolved state in both
  portals.
- Connect top-up history to settled usage in dashboard/billing views.

Exit criteria:

- Customers can see what they reserved, what they actually paid, and what
  was refunded.
- Operators can inspect the same request from the admin side.

### Phase 3 - extend beyond chat

Goals:

- Apply the same settlement model to embeddings, images, speech, and
  transcription routes.

Changes:

- Reuse the settlement service across all billable routes.
- Normalize capability-specific work-unit handling behind one settlement
  interface.

## 4. Design notes

- `app.reservations` is the immediate source of truth for customer-facing
  reconciliation.
- A dedicated immutable `usage_records` table may still be added later if
  audit depth or analytics needs exceed what the reservation ledger can
  provide.
- Resolver inventory already carries `interaction_mode` via manifest
  extras; the gateway should consume that instead of inferring support
  from offering names.

## 5. Current implementation order

1. Land Phase 1A in code.
2. Validate route selection and playground behavior with tests.
3. Begin Phase 1B without waiting for further user confirmation unless a
   schema or contract blocker appears.
