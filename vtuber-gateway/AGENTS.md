# AGENTS.md

This is `vtuber-gateway/` — the customer-facing vtuber gateway in the
rewrite. TypeScript + Fastify 5 + Zod + drizzle-orm + ESM. Owns:
session-open + control-WS relay + per-session `payment-daemon`
emission + Stripe checkout (delegated to `customer-portal/`) + a
vtuber-specific portal SPA.

The plan brief is
[`../docs/exec-plans/completed/0013-vtuber-suite-migration.md`](../docs/exec-plans/completed/0013-vtuber-suite-migration.md);
read §4.1 (component layout), §5.1 (source-to-destination map),
§7.1 (DB schema), §8.1 (routes), §9.1 (deps), §10.1 (env vars), and
§14 + OQ1-OQ5 (locked decisions) before structural changes.

Component-local agent map. Repo-root [`../AGENTS.md`](../AGENTS.md) is
the cross-cutting map.

## Operating principles

Inherited from the repo root + `customer-portal/`. Plus:

- **Imports `@livepeer-rewrite/customer-portal` for SaaS-shell
  primitives** — auth (`sessionBearer`), Stripe checkout + webhook,
  admin engine, Postgres pool. The shell owns `app.*`; this gateway
  owns `vtuber.*` (per Q5 lock).
- **Per-session `payerDaemon.createPayment(...)` flow** (Q7 lock) —
  one ticket per session-open with face-value seeded from
  `LIVEPEER_PAYER_DEFAULT_FACE_VALUE_WEI`; per-second debits accrue
  against it; top-up extends the same session.
- **Worker-control bearer auth — HMAC-SHA-256 with pepper, hash-stored**
  (Q8 lock). Format `vtbsw_<43-char-base64url>`. Bearer is per-session,
  short-lived, minted by this gateway and verified by the runner.
- **Session-bearer (customer-side) is `vtbs_<43-char-base64url>`** —
  HMAC-SHA-256 with pepper, hash-stored (suite parity).
- **Live-only control-WS relay; no replay buffer in M6** (Q9 lock).
  Customer reconnects = `cannot_resume`. 30s replay buffer is a
  follow-up commit.
- **Vtuber-specific portal pages live in `src/frontend/portal/`**
  (OQ3 lock) — the shared shell only ships common widgets; product
  pages compose those primitives.
- **`bridge` and `BYOC` terminology eradicated** in narrative; suite
  citations preserve historical names verbatim.

## Where to look

| Question | File |
|---|---|
| What is this component? | [`README.md`](./README.md) |
| Plan brief | [`../docs/exec-plans/completed/0013-vtuber-suite-migration.md`](../docs/exec-plans/completed/0013-vtuber-suite-migration.md) |
| DB schema source | [`src/repo/schema.ts`](./src/repo/schema.ts) |
| Migrations | [`migrations/`](./migrations/) |
| Vtuber portal SPA | [`src/frontend/portal/`](./src/frontend/portal/) |
| Routes | [`src/routes/`](./src/routes/) |

## Layout

```
src/
  index.ts                ← composition root (calls payment.init, listen)
  config.ts               ← Zod env schema (vtuber-specific)
  server.ts               ← Fastify factory
  config/                 ← per-domain config helpers
  types/                  ← session-open Zod schemas
  repo/                   ← drizzle queries (vtuber.sessions, vtuber.session_bearers, vtuber.usage_records, vtuber.node_health, schema.ts)
  providers/              ← payment-daemon + service-registry-daemon clients
  service/
    auth/                 ← sessionBearer (vtbs_*) + workerControlBearer (vtbsw_*)
    sessions/             ← openSession / closeSession / topupSession (per-session payer flow)
    relay/                ← live worker↔customer WS relay (no buffer in M6)
    payments/             ← payerDaemon.createPayment wrapper
    nodes/                ← vtuber-capable node roster + scheduler
    billing/              ← per-second rollups (vtuber rate-card)
  routes/                 ← POST/GET /v1/vtuber/sessions, /:id/end, /:id/topup, WS /:id/control, billing-topup, stripe-webhook
  livepeer/               ← wire layer (gateway-adapters consumer)
  runtime/                ← runtime helpers
  frontend/portal/        ← vtuber-specific portal SPA (composes customer-portal shared widgets)
migrations/
  0000_vtuber_init.sql    ← vtuber.sessions, vtuber.session_bearers, vtuber.usage_records, vtuber.node_health, vtuber.session_payer_work_id, vtuber.rate_card_session
test/
  unit/
  integration/
  smoke/
```

## Source attribution

Code is ported from
`livepeer-network-suite/livepeer-vtuber-gateway/src/` (TypeScript) per
plan 0013-vtuber §5.1. The schema is renumbered + namespaced to
`vtuber.*` (suite shipped flat tables in `public.*`). Quote-related
code (`quoteRefresher`, `serviceRegistry`) is **dropped** per the
quote-free flow; broker URL is the only resolver.
