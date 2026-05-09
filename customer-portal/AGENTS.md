# AGENTS.md — customer-portal

This is `customer-portal/` — the shared SaaS-shell library for per-product
gateways in the rewrite (`openai-gateway/`, `vtuber-gateway/`,
`video-gateway/`). Distributed as `@livepeer-rewrite/customer-portal` via
the pnpm workspace; consumers import factories from subpath exports.

The plan brief is
[`../docs/exec-plans/completed/0013-shell-customer-portal-extraction.md`](../docs/exec-plans/completed/0013-shell-customer-portal-extraction.md).
Read §4, §5, §7, §13, §14 before making structural changes.

## Operating principles

Inherited from the repo root. Plus:

- **Library code, not a service.** Imported by per-product gateways into
  their Fastify app. The Dockerfile is for the test/build environment,
  not production deployment (per core belief #15).
- **One workspace package.** Q1 + Q2 locks: single shared shell, no
  OSS-vs-SaaS split, no separate `-core` engine.
- **Per-product isolation.** OQ3 lock: each gateway brings its own
  Postgres / Redis / Stripe creds / API-key pepper. The shell never
  hardcodes credentials and never assumes shared state.
- **No default `RateCardResolver` impl.** OQ2 lock: per-product gateway
  always wires its own pricing.
- **Schema namespace `app.*`.** Q6 lock: shell owns `app.*`; per-product
  schemas live alongside (`openai.*`, `vtuber.*`, `media.*`).

## Where to look

| Question | File |
|---|---|
| What is this library? | [`README.md`](./README.md) |
| Library design | [`DESIGN.md`](./DESIGN.md) |
| Plan brief | [`../docs/exec-plans/completed/0013-shell-customer-portal-extraction.md`](../docs/exec-plans/completed/0013-shell-customer-portal-extraction.md) |
| Build / test gestures | [`Makefile`](./Makefile) |
| DB schema source | [`src/db/schema.ts`](./src/db/schema.ts) |
| Migration files | [`migrations/`](./migrations/) |
| Frontend sub-workspace | [`frontend/`](./frontend/) |

## Layout

```
src/
  auth/          API-key gen/hash/verify, AuthResolver
  billing/       Wallet, reservations, top-ups
  billing/stripe/ StripeClient interface, checkout, webhook handler
  middleware/    Fastify pre-handlers (auth, rate-limit, wallet-reserve)
  admin/         Operator basic-auth admin engine
  db/            drizzle pgSchema('app'), migrate.ts, pool.ts
  repo/          drizzle queries (customers, api_keys, reservations, …)
  service/       authenticate, pricing (RateCardResolver iface), admin engine
  config/        Zod env schemas
  testing/       Wallet fakes, Stripe stub, test pool factories
migrations/      drizzle SQL (numbered 0000..NNNN)
frontend/        pnpm sub-workspace (shared/admin/portal UI packages)
test/            integration + smoke
```

## Frontend invariant

All frontend work under `frontend/` must follow the cross-cutting repo rule in
[`../docs/design-docs/frontend-dom-and-css-invariants.md`](../docs/design-docs/frontend-dom-and-css-invariants.md).

That rule is strict:

- light DOM only
- semantic HTML only
- no inline CSS
- styling only from checked-in CSS files

The frontend migration recorded in
[`0023-strict-frontend-dom-and-css-invariants`](../docs/exec-plans/completed/0023-strict-frontend-dom-and-css-invariants.md)
is complete.

- do not add shadow-DOM UI code
- do not add `static styles = css` blocks
- do not add `style=` attributes
- treat any new frontend invariant violation as a bug

## Doing work

- **TypeScript with strict types.** `tsc` is the source of truth; tests run
  via `node --test` against `dist/`.
- **No emojis.** No comments narrating WHAT the code does. No plan-number
  references in code comments.
- **Suite-source attribution** lives in commit messages and this file
  (below), per repo-root AGENTS.md lines 62-66.
- **The shell exposes interfaces; per-product gateways implement them.**
  `Wallet`, `AuthResolver`, `AdminAuthResolver`, `RateLimiter`,
  `RateCardResolver`, `StripeClient`.

## Suite-source attribution

This package ports code from the suite (`livepeer-network-suite/`) under
explicit user instruction (plan brief §5). Source paths:

- `livepeer-openai-gateway/packages/livepeer-openai-gateway/src/repo/schema.ts`
  — drizzle schema (excluding `rate_card_*` and `retail_price_*` tables;
  those stay in per-product gateways per Q7 lock).
- `livepeer-openai-gateway/packages/livepeer-openai-gateway/migrations/0000_app_init.sql`
  — initial `app.*` schema migration.
- `livepeer-openai-gateway/packages/livepeer-openai-gateway/migrations/0003_idempotency_requests.sql`
  — idempotency table migration (renumbered to `0001_idempotency_requests.sql`).
- `livepeer-openai-gateway/packages/livepeer-openai-gateway/src/service/auth/{keys,authenticate,cache}.ts`
  — API-key generation/hashing/verification + TTL cache.
- `livepeer-openai-gateway/packages/livepeer-openai-gateway/src/service/billing/{wallet,reservations,topups}.ts`
  — Wallet impl + reservation CRUD + top-up credit logic.
- `livepeer-openai-gateway/packages/livepeer-openai-gateway/src/repo/{customers,apiKeys,reservations,topups,stripeWebhookEvents,adminAuditEvents}.ts`
  — drizzle queries.
- `livepeer-openai-gateway/packages/livepeer-openai-gateway/src/runtime/http/billing/topup.ts`
  — Checkout-session route helper.
- `livepeer-openai-gateway/packages/livepeer-openai-gateway/src/runtime/http/stripe/webhook.ts`
  — Stripe webhook handler.
- `livepeer-openai-gateway/packages/livepeer-openai-gateway/src/providers/stripe.ts` +
  `src/providers/stripe/sdk.ts` — `StripeClient` interface + SDK impl.
- `livepeer-openai-gateway-core/src/runtime/http/middleware/{auth,rateLimit}.ts`
  — Fastify pre-handlers.
- `livepeer-openai-gateway-core/src/service/admin/basicAuthResolver.ts`
  — operator basic-auth resolver.
- `livepeer-openai-gateway-core/src/interfaces/{wallet,caller,authResolver,rateLimiter,adminAuthResolver}.ts`
  — interface shapes.
- `livepeer-openai-gateway/frontend/{shared,portal,admin}/`
  — initial frontend source port. The rewrite kept the shared-shell product scope while
  later migrating the implementation to the repo-standard light-DOM + external-CSS
  architecture.

## What lives elsewhere

- Per-product gateway briefs: `0013-openai`, `0013-vtuber`, `0013-video`.
- Wire-protocol middleware: `gateway-adapters/` (plan 0008 + followup).
- Chain-side payment: `payment-daemon/` (plans 0014, 0016).
- Multi-tenant operator console: `secure-orch-console/` (plan 0019).
