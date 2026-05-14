---
plan: 0013-vtuber-followup
title: vtuber-gateway Phase 4 wire-through + M6 reconnect-30s + bridge-* purge — design
status: completed
phase: shipped
opened: 2026-05-07
closed: 2026-05-07
owner: harness
related:
  - "completed plan 0013-vtuber — parent suite-migration brief; §14 carries 15 prior locks"
  - "completed plan 0013-shell — customer-portal/ provides authPreHandler, Stripe, repo modules consumed here"
  - "completed plan 0012 — session-control-plus-media driver; this followup mirrors its reconnect-window state model"
  - "completed plan 0012-followup — broker-side replay buffer + Last-Seq pattern"
  - "user-memory feedback_no_bridge_term — hard ban on the word 'bridge' in narrative"
audience: vtuber-gateway maintainers, vtuber-pipeline maintainers
---

# Plan 0013-vtuber-followup — Phase 4 wire-through + M6 reconnect + bridge-* purge

> **Implementation followup.** Plan 0013-vtuber landed the scaffold,
> schema, peppered HMAC bearer, live-only relay, and 503 stubs at the
> route layer. This brief wires the stubs to real implementations,
> ships the M6 control-WS reconnect-30s window, and purges the
> inherited `bridge-*` symbol names from `vtuber-pipeline/.../gateway.py`.
> Locks for the five open questions are in §14; the implementing agent
> works against those locks.

## 1. Status and scope

Scope: **Phase 4 wire-through + M6 + symbol-purge.** Five work items:

1. Session-open route → real `serviceRegistry.select` + `payerDaemon.createPayment` + drizzle session row insert + worker `/api/sessions/start` dispatch.
2. Session-end / topup → worker dispatch + drizzle status flip + billing finalize / topup-extension.
3. Billing-topup + Stripe webhook → delegate to `customer-portal/`'s `createTopupCheckoutSession` + `handleStripeWebhook`.
4. Control-WS M6 reconnect-30s window with replay buffer, Last-Seq replay, race-on-handshake conflict policy, window-expiry teardown.
5. `bridge-*` symbol purge in `vtuber-pipeline/src/vtuber_pipeline/streams/providers/gateway.py` and call sites.

Out of scope:

- Schema / migration changes (locked by 0013-vtuber §7.1).
- Wire-protocol changes — `vtuber-gateway` consumes `gateway-adapters/` and `livepeer-network-protocol/` shapes verbatim.
- Vtuber-specific Stripe products (persona packs, session-time extensions) — Q4 lock keeps the webhook handler scope to top-up flow only; product-layer commerce belongs in `vtuber-pipeline/`.
- All other followups; this brief stays in vtuber-gateway and the one Python file.

## 2. What plan 0013-vtuber left unfinished

Plan 0013-vtuber landed the gateway shape: `src/routes/{sessions,billingTopup,stripeWebhook,sessionControlWs}.ts` ship as 503-stub Fastify plugins; `src/providers/{payerDaemon,serviceRegistry,workerClient}.ts` ship as **interface-only** modules with no concrete client; `src/service/auth/sessionBearer.ts` ships HMAC bearer mint/hash crypto; `src/service/relay/sessionRelay.ts` ships the live-only worker↔customer relay (Q9 lock — no buffer in M6).

The implementing agent at plan 0013-vtuber's Phase 5 closeout flagged two debts: (a) the route handlers need to call into the providers + drizzle; (b) the M6 control-WS needs the reconnect-30s window per the suite spec. Both land here.

The `bridge-*` symbol residue in `vtuber-pipeline/src/vtuber_pipeline/streams/providers/gateway.py` was kept as-is at suite-port time; per `feedback_no_bridge_term` user-memory the term is banned in narrative and the rename has been deferred to this followup.

## 3. Reference architecture

Same as plan 0013-vtuber §3 (no shape change). The wire-through fills in the dotted boxes:

```
  POST /v1/vtuber/sessions
    └── authPreHandler (customer-portal API-key)
         └── serviceRegistry.select(vtuber-session, offering)  ← Q2: 503+Retry-After on no-worker
              └── mintSessionBearer + drizzle insert(session, session_bearer)
                   └── payerDaemon.createPayment(faceValueWei, recipient, capability, offering, nodeId)
                        └── workerClient.startSession(nodeUrl, X-Livepeer-Payment, child-bearer)
                             └── drizzle update session.status='active'
                                  └── 200 SessionOpenResponse
```

WS upgrade `/v1/vtuber/sessions/:id/control` keeps the live relay, but
adds a per-session reconnect-window state machine per Q3 (§8 below).

## 4. Auth wiring at route handlers

Per **Q1 lock** the route-level dispatch is:

| Route | Auth |
| --- | --- |
| `POST /v1/vtuber/sessions` (session-open) | API-key (`customer-portal` `authPreHandler`) |
| `GET /v1/vtuber/sessions/:id` | session-bearer (`vtbs_*` lookup against `vtuber_session_bearer`) |
| `POST /v1/vtuber/sessions/:id/end` | session-bearer |
| `POST /v1/vtuber/sessions/:id/topup` | session-bearer |
| `WS /v1/vtuber/sessions/:id/control` | session-bearer |
| `POST /v1/billing/topup` | API-key |
| `POST /v1/stripe/webhook` | none (Stripe signature is the proof) |

Constant-time compare on the bearer hash; the lookup is a single
indexed read against `vtuber_session_bearer.hash`.

Pipeline meta-customer flow uses the API-key path with the shared
deployment-level key (per plan 0013-vtuber §3, `LIVEPEER_VTUBER_GATEWAY_API_KEY`).

## 5. Session-open wiring

`POST /v1/vtuber/sessions`:

1. `customer-portal.authPreHandler` resolves the caller (Q1).
2. Validate body via `SessionOpenRequestSchema` (already in tree).
3. `serviceRegistry.select({capability: "livepeer:vtuber-session", offering})` — returns `{nodeId, nodeUrl, ethAddress}` or `null`.
4. **Q2 lock**: if no worker, `503` + `Retry-After: 5` + `Livepeer-Error: no_worker_available`. Reserve nothing (no payment minted).
5. `mintSessionBearer(pepper)` → `{bearer, hash}`.
6. drizzle `INSERT INTO vtuber.session` with `status='starting'`, `node_id`, `node_url`, `expires_at`, `params_json`. drizzle `INSERT INTO vtuber.session_bearer` with `(session_id, customer_id, hash)`.
7. `payerDaemon.createPayment({faceValueWei, recipient: ethAddress, capability: "livepeer:vtuber-session", offering, nodeId})` → `{payerWorkId, paymentHeader}`. drizzle `UPDATE vtuber.session SET payer_work_id=...`.
8. `workerClient.startSession(nodeUrl, {...request, worker_control_bearer: <minted>, X-Livepeer-Payment: paymentHeader})`. On success drizzle `UPDATE vtuber.session SET status='active', worker_session_id=...`.
9. Return `{session_id, control_url, expires_at, session_child_bearer: bearer}` (200).

Failure paths: payerDaemon.createPayment failure → drizzle `UPDATE vtuber.session SET status='errored', error_code='payment_emit_failed'`, return 502 + `Livepeer-Error: payment_emit_failed`. Worker-start failure → drizzle errored + `worker_start_failed`, payment refunded if the daemon supports it (deferred).

## 6. Session-end wiring

`POST /v1/vtuber/sessions/:id/end` (session-bearer auth):

1. session-bearer middleware looks up the bearer hash, sets `req.session = {...}`.
2. Validate `:id === req.session.id` (defense-in-depth).
3. `workerClient.stopSession(nodeUrl, worker_session_id)` (best-effort; ignore errors).
4. drizzle `UPDATE vtuber.session SET status='ended', ended_at=now()`.
5. (Plan 0013-vtuber's `vtuberBilling.finalizeSession` lives in `src/service/billing/`; placeholder OK if billing module not yet wired — finalize the session-payer work-unit total, no rollup change.)
6. `relay.endAll(sessionId)` to close any live WS.
7. Return 200 `{session_id, status: "ended", ended_at}`.

## 7. Topup wiring

`POST /v1/vtuber/sessions/:id/topup` (session-bearer auth):

1. session-bearer auth.
2. Validate `cents > 0`.
3. Convert `cents → faceValueWei` per the rate-card (or pass through to a future helper; v0.1 uses a pinned conversion).
4. `payerDaemon.createPayment(faceValueWei, recipient, capability, offering, nodeId)` — issues a fresh ticket against the same `node_id` / `offering` / `capability`.
5. `workerClient` POST `/api/sessions/{worker_session_id}/topup` (passes the new `X-Livepeer-Payment` header).
6. Return 200 `{session_id, faceValueWei, payerWorkId}`.

## 8. M6 control-WS reconnect-30s window

Per **Q3 lock** the gateway mirrors capability-broker's
session-control-plus-media exactly. Three flags:

- `--vtuber-control-reconnect-window=30s` (matches broker's `--session-control-reconnect-window`).
- `--vtuber-control-reconnect-buffer-messages=64` (matches broker default).
- `--vtuber-control-reconnect-buffer-bytes=1048576` (1 MiB; matches broker default).

State model:

- **Per-session reconnect state** persists in-process for the window duration:
  - `disconnAt: Date | null` — wall-clock when the active customer WS dropped.
  - `replay: ReplayBuffer` — bounded ring of server-emitted envelopes with `seq` + `payload` (drops oldest under either cap, suite-parity).
  - `nextSeq: number` — broker-monotonic seq applied to every server-emitted envelope; resumes across reconnects.
  - `active: boolean` — whether a WS is currently bound.
- **On WS drop**: hold session state for `--reconnect-window`; runner stays alive (worker WS remains connected); payment-daemon ticker keeps billing.
- **On reconnect**: customer connects to same `/v1/vtuber/sessions/:id/control` with same session-bearer. Reads `Last-Seq` header (or `last_seq` query). Replay buffered server-emitted entries with `seq > Last-Seq`.
- **Race policy**: first-to-complete-handshake wins; loser gets `409 Conflict` (same shape as broker's `session already attached`).
- **Window expiry**: full session teardown — `worker.stopSession`, drizzle status flip, `relay.endAll`, finalize billing.

`reconnectWindow.ts` lives at `src/service/relay/reconnectWindow.ts` next to `sessionRelay.ts`. Public surface: `createReconnectWindow(cfg)` returns `{onCustomerAttach, onCustomerDrop, onWindowExpiry, replay, nextSeq}` keyed by `sessionId`.

## 9. Stripe + billing-topup wiring

`POST /v1/billing/topup` (API-key auth — Q1):

1. Validate `{cents, success_url, cancel_url}`.
2. Resolve `customer-portal.stripe.createTopupCheckoutSession(stripe, config, {customerId, amountUsdCents, successUrl, cancelUrl})`.
3. Return `{stripe_checkout_url, stripe_session_id}`.

`POST /v1/stripe/webhook` (no auth — Q1; Stripe signature is the proof):

1. Read raw body (Fastify body is parsed by default; we register a raw-body parser scoped to this route only — same shape used in `customer-portal` tests).
2. `customer-portal.stripe.handleStripeWebhook({store, stripe}, {rawBody, signature})`.
3. Map outcomes: `processed` → 200 `{outcome: "processed"}`; `duplicate` → 200; `signature_invalid` → 400; `handler_error` → 500; `unsupported` → 200.
4. **Q4 lock**: only the top-up flow processes. VTuber-specific Stripe products (persona packs, session-time extensions) live in `vtuber-pipeline/`; this gateway is the protocol gateway and only knows about credits being added.

## 10. `bridge-*` symbol purge

Per **Q5 lock** full purge in `vtuber-pipeline/src/vtuber_pipeline/streams/providers/gateway.py`:

- `HTTPBridgeClient` → `HTTPGatewayClient`.
- `BridgeError` → `GatewayError`.
- `BridgeClient` → `GatewayClient`.
- `BridgeSessionOpenResult` → `GatewaySessionOpenResult`.
- `bridge_session_id` → `gateway_session_id` (DTO field).
- All in-file `bridge` terminology in docstrings → `gateway`.

Call sites updated in the same commit:

- `vtuber-pipeline/src/vtuber_pipeline/streams/providers/__init__.py` re-exports.
- `vtuber-pipeline/src/vtuber_pipeline/streams/runtime/entrypoint.py` import + construction.
- `vtuber-pipeline/src/vtuber_pipeline/streams/service/__init__.py` lifecycle import + injected param.
- `vtuber-pipeline/src/vtuber_pipeline/streams/types/__init__.py` DTO field.
- `vtuber-pipeline/src/vtuber_pipeline/streams/ui/__init__.py` field reads.
- `vtuber-pipeline/tests/test_streams_lifecycle.py` test references + fake.
- `vtuber-pipeline/src/vtuber_pipeline/streams/__init__.py` docstring (narrative).
- `vtuber-pipeline/src/vtuber_pipeline/streams/config/__init__.py` config fields (`bridge_url` → `gateway_url`, `bridge_public_url` → `gateway_public_url`, `bridge_customer_bearer` → `gateway_customer_bearer`; corresponding env-var names follow).

`vtuber-pipeline/AGENTS.md` and `README.md` narrative is updated to drop the `bridge_session_id` historical note that this followup retires.

Any `STREAMS_BRIDGE_*` env-var names are renamed to `STREAMS_GATEWAY_*`. Single commit, no fallback (this is a pre-1.0 internal pipeline; no ops surface to break).

## 11. Tests

vtuber-gateway:

- `test/integration/routes.test.ts` — extended for happy + no-worker + payerDaemon-fail + worker-start-fail (new fakes for the three providers).
- `test/integration/billing.test.ts` (new) — billing-topup happy path with a fake `StripeClient`.
- `test/integration/stripeWebhook.test.ts` (new) — webhook round-trip with `stripeMock` from `customer-portal/testing`.
- `test/unit/relay/reconnectWindow.test.ts` (new) — drop+reconnect+replay; drop+expiry teardown; race-on-handshake conflict.
- Existing relay + sessionBearer tests stay green.

vtuber-pipeline:

- `tests/test_streams_lifecycle.py` — symbol rename only (assertions stay).
- pytest must pass after rename.

Cross-component:

- `go test ./...` from `payment-daemon/` + `capability-broker/` stays green (no Go touches in this followup).

## 12. Out of scope (deferred)

- v1.0 chain integration — gates on plan 0016.
- Vtuber-specific Stripe products (persona packs) — product-layer commerce in vtuber-pipeline.
- Multi-region service-registry — single-region select-first in v0.1.
- Worker WS ↔ customer WS reconnect on the **worker** side — runner crash is teardown.
- Audit-log emission on every wire-through call — flagged for follow-up; v0.1 uses pino structured logs.

## 13. Implementing-agent commit cadence

The implementing agent ships in **6 commits** over five phases:

1. **Phase 1 — `docs(plan-0013-vtuber-followup): author brief with 5 DECIDED locks`.** This document.
2. **Phase 2 — `feat(vtuber-gateway): wire session-open through payerDaemon + serviceRegistry + drizzle`.** §5 above; route-level tests.
3. **Phase 3 — `feat(vtuber-gateway): wire session-end + topup + stripe-webhook`.** §6 + §7 + §9; route-level tests + Stripe replay test.
4. **Phase 4 — `feat(vtuber-gateway): M6 control-WS reconnect-30s window + replay buffer`.** §8; `relay/reconnectWindow.test.ts`.
5. **Phase 5 — `refactor(vtuber-pipeline): purge bridge-* symbol names per user-memory ban`.** §10; pytest stays green.
6. **Phase 6 — `docs(plan-0013-vtuber-followup): plan close`.** Move active → completed; do not touch `PLANS.md`.

`pnpm -F @livepeer-network-modules/vtuber-gateway build` + `test` are green at every commit. `vtuber-pipeline` pytest passes after the rename. `go test ./...` stays untouched.

## 14. Resolved decisions

All five questions were resolved by user walk-through on 2026-05-07.
The implementing agent works against these locks; rationale captured
for future readers.

### Q1. Auth wiring at route handlers

**DECIDED: route-level dispatch.**

- `POST /v1/vtuber/sessions` (session-open) → API-key auth via `customer-portal`'s `authPreHandler`.
- `GET /v1/vtuber/sessions/:id`, `POST /v1/vtuber/sessions/:id/end`, `POST /v1/vtuber/sessions/:id/topup`, `WS /v1/vtuber/sessions/:id/control` → session-bearer auth (lookup in `vtuber.session_bearer` table; constant-time compare against `Authorization: Bearer vtbs_*`).
- `POST /v1/billing/topup` → API-key auth.
- `POST /v1/stripe/webhook` → no auth (Stripe signature is the proof).

Pipeline meta-customer flow uses the API-key path with the shared
deployment-level key. Route-level dispatch keeps the auth pattern
visible at the route registration site (no global pre-handler magic);
matches the pattern `customer-portal/test/middleware.test.ts` documents.

### Q2. `serviceRegistry.select` no-worker behavior

**DECIDED: 503 + `Retry-After: 5` + `Livepeer-Error: no_worker_available`.**

Don't fail-open. Reserve no payment for a session that can't run.
`Retry-After: 5` is the broker convention for transient pool exhaustion
(matches `livepeer-network-protocol/headers/livepeer-headers.md`). The
`Livepeer-Error: no_worker_available` header is the standard
machine-readable signal so customers retry against the same gateway.

### Q3. Reconnect-30s window state model

**DECIDED: mirror capability-broker's session-control-plus-media exactly.**

- New flag `--vtuber-control-reconnect-window=30s` (matches broker's `--session-control-reconnect-window`).
- New flag `--vtuber-control-reconnect-buffer-messages=64`.
- New flag `--vtuber-control-reconnect-buffer-bytes=1048576` (1 MiB).
- Replay buffer 64 messages or 1 MiB JSON, whichever fills first; oldest dropped on overflow (matches `capability-broker/internal/modes/sessioncontrolplusmedia/controlws_reconnect.go`).
- Customer reconnects with `Last-Seq` header; gateway replays buffered server-emitted messages with `seq > Last-Seq`.
- Race policy: **first-to-complete-handshake wins; loser gets 409**.
- On window expiry: full session teardown (session-end, finalize billing, runner shutdown).

The exact-mirror posture eliminates a divergence that would otherwise
have to be reconciled later; the broker pattern is suite-validated and
unit-test-covered.

### Q4. Stripe webhook handler scope

**DECIDED: top-up flow only in v0.1.**

VTuber-specific Stripe products (persona packs, session-time
extensions) are product-layer concerns that live in `vtuber-pipeline/`;
`vtuber-gateway` is the protocol gateway and only needs to know about
credits being added. The `customer-portal` `handleStripeWebhook`
already discriminates on `event.type` — vtuber product types are
unsupported (return `outcome: 'unsupported'`, 200). The pipeline
ships its own Stripe surface against its own customers when the
product layer needs it.

### Q5. `bridge-*` symbol cleanup scope in `vtuber-pipeline/.../gateway.py`

**DECIDED: full purge.**

Rename ALL public + internal symbols. `HTTPBridgeClient` →
`HTTPGatewayClient`. `BridgeError` → `GatewayError`. `BridgeClient`
→ `GatewayClient`. `BridgeSessionOpenResult` →
`GatewaySessionOpenResult`. `bridge_session_id` → `gateway_session_id`
(DTO field). All other "bridge" terminology in this file (and any
related test files / type imports / docstrings) → "gateway". Single
commit at the end of the followup. Per user-memory
`feedback_no_bridge_term` hard ban; no fallback aliasing. The pipeline
is pre-1.0 internal; no operator surface breaks.

Env-var renames included: `STREAMS_BRIDGE_URL` →
`STREAMS_GATEWAY_URL`, `STREAMS_BRIDGE_PUBLIC_URL` →
`STREAMS_GATEWAY_PUBLIC_URL`, `STREAMS_BRIDGE_CUSTOMER_BEARER` →
`STREAMS_GATEWAY_CUSTOMER_BEARER`.

## 15. Out of scope (deferred)

- All other in-flight followups; this brief is single-component.
- Production cutover gating on plan 0016 (chain-integrated payer-daemon).
- Schema migration to roll up Stripe events per vtuber product type.
- Operator runbook updates beyond the existing 0013-vtuber doc.

---

## Appendix A — file paths cited

This monorepo:

- `vtuber-gateway/src/routes/sessions.ts` (existing 503 stubs; this followup wires).
- `vtuber-gateway/src/routes/billingTopup.ts` (existing 503 stub; this followup wires).
- `vtuber-gateway/src/routes/stripeWebhook.ts` (existing 503 stub; this followup wires).
- `vtuber-gateway/src/routes/sessionControlWs.ts` (existing live-only relay; this followup adds reconnect-window).
- `vtuber-gateway/src/providers/{payerDaemon,serviceRegistry,workerClient}.ts` (interfaces; this followup adds concrete clients or test fakes per scope).
- `vtuber-gateway/src/service/auth/sessionBearer.ts` (existing HMAC bearer crypto; this followup uses).
- `vtuber-gateway/src/service/relay/sessionRelay.ts` (existing live-only relay; reconnect window is a sibling module).
- `vtuber-gateway/src/repo/{schema,vtuberSessions,vtuberSessionBearers}.ts` (existing drizzle queries; this followup uses).
- `customer-portal/src/middleware/authPreHandler.ts` (consumed for API-key auth).
- `customer-portal/src/billing/stripe/{checkout,webhook}.ts` (consumed for billing-topup + webhook).
- `capability-broker/internal/modes/sessioncontrolplusmedia/controlws_reconnect.go` (replay-buffer reference).
- `capability-broker/internal/modes/sessioncontrolplusmedia/controlws.go` (Last-Seq header reference).
- `capability-broker/cmd/livepeer-capability-broker/main.go:149-163` (flag-name reference).
- `vtuber-pipeline/src/vtuber_pipeline/streams/providers/gateway.py` (the `bridge-*` purge target).
- `vtuber-pipeline/src/vtuber_pipeline/streams/{providers,runtime,service,types,ui,config}/__init__.py` (call sites).
- `vtuber-pipeline/tests/test_streams_lifecycle.py` (test references).

Cross-plan references in this monorepo:

- `docs/exec-plans/completed/0013-vtuber-suite-migration.md` (parent; §14 + OQ1-OQ5 prior locks).
- `docs/exec-plans/completed/0013-shell-customer-portal-extraction.md` (provides Stripe + auth modules consumed here).
- `docs/exec-plans/completed/0012-followup-session-control-media-plane.md` (broker-side reconnect-window precedent that Q3 mirrors).
