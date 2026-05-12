---
plan: 0003-portal-model-centric-catalog-and-availability-driven-selection
title: portal model-centric catalog and availability-driven selection
status: completed
opened: 2026-05-11
completed: 2026-05-11
owner: harness
audience: openai-gateway maintainers
related:
  - "active plan 0002-openai-compatible-playground-surface"
  - "completed plan 0022-openai-gateway-ui-parity"
  - "customer portal playground catalog"
  - "resolver-backed route selection"
---

# Plan 0003 - portal model-centric catalog and availability-driven selection

## Closeout

This plan is complete.

The `openai-gateway` customer playground now:

- consumes a portal-specific model-centric catalog from `/portal/playground/catalog`
- shows one top-level entry per public model rather than one row per discovered route
- unions `supported_modes` at the model level
- retains route variants under each model for selector-driven routing
- derives selector headers from the chosen variant
- explains selector policy in the portal UI

OpenAI-facing `/v1/*` behavior remains unchanged:

- `/v1/models` stays flat and OpenAI-facing
- `/v1/chat/completions` mode handling is unchanged
- no orch or broker URL pinning was introduced

The implementation also landed regression coverage for:

- merged model catalog shape
- model-level stream availability
- mode-aware variant selection
- selector-header derivation from selected variants

## 1. Why this plan existed

The customer playground previously consumed a flat route-derived catalog and stored
selection by public model id alone. That broke down when the same public model was
offered through multiple variants such as:

1. non-streaming chat via one offering
2. streaming chat via another offering
3. future provider, price, or constraint variants under the same public model id

The result was a portal that could misrepresent what was actually available even while
the gateway API itself could route correctly.

## 2. End state achieved

After this work:

- `/portal/playground/catalog` returns a portal-specific, model-centric catalog.
- Each public model appears once per capability in the portal catalog.
- Each model advertises unioned `supported_modes`.
- Each model includes discovered route variants with selector-driving metadata.
- The portal chooses request behavior from discovered availability rather than from
  ad hoc assumptions.
- Portal requests pass variant-derived selector headers so the runtime selection policy
  reflects the discovered choice.
- OpenAI-compatible `/v1/*` request and response semantics remain unchanged.
- `/v1/models` remains flat and OpenAI-facing.

## 3. Non-goals retained

This plan did not:

1. pin requests to a specific orch address
2. pin requests to a specific broker URL
3. change gateway dispatch or resolver semantics
4. change billing or payment settlement behavior
5. change the OpenAI-compatible request or response shape of `/v1/*`
6. change `/v1/models` into a portal-specific shape

## 4. Compatibility guardrail honored

This work did not change the semantics of:

- `POST /v1/chat/completions`
- `POST /v1/embeddings`
- `POST /v1/audio/transcriptions`
- `POST /v1/audio/speech`
- `GET /v1/realtime`
- `GET /v1/models`
- SSE response framing for streaming chat

## 5. What shipped

### 5.1 Portal-specific normalized catalog

The gateway now has a portal catalog builder that groups discovered candidates by:

- capability
- public model id

Each model entry exposes:

- `model_id`
- `capability`
- `supported_modes`
- one capability `surface`
- `variants[]`

Each variant exposes:

- `selection_key`
- `offering`
- `supported_modes`
- `broker_url`
- `eth_address`
- `price_per_work_unit_wei`
- `work_unit`
- `extra`
- `constraints`

### 5.2 Availability-driven portal behavior

The portal no longer treats public model id as a proxy for one route row.

Instead it:

1. selects a public model
2. uses model-level discovered `supported_modes`
3. derives requested mode from the payload
4. selects a compatible variant from that model's `variants[]`
5. passes selector headers from that variant

### 5.3 Selector semantics in the portal

The portal now reflects existing gateway selector semantics explicitly:

- `Livepeer-Selector-Constraints` as hard filter
- `Livepeer-Selector-Extra` as ranking hint
- `Livepeer-Selector-Max-Price-Wei` as optional hard ceiling

The portal UI also explains:

- constraints are hard requirements
- extra is a soft preference
- exact orch or broker pinning is not part of this flow

### 5.4 `/v1/models` stayed flat

The portal catalog and `/v1/models` continue serving different consumers:

- `/v1/models` remains a flat, OpenAI-facing list
- `/portal/playground/catalog` is the richer portal read model

## 6. Verification

Validation completed with:

1. `pnpm test`
2. `pnpm build`

Regression coverage now exists for the original duplicate-model / wrong-mode issue and
for selector-header derivation from selected variants.
