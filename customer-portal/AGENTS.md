# AGENTS.md — customer-portal

This is `customer-portal/` — the shared SaaS-shell library distributed as
`@livepeer-network-modules/customer-portal` via the pnpm workspace;
consumers import factories from subpath exports.

## Operating principles

Inherited from the repo root. Plus:

- **Library code, not a service.** Imported by consumer services into
  their Fastify app. The Dockerfile is for the test/build environment,
  not production deployment (per core belief #15).
- **One workspace package.** Q1 + Q2 locks: single shared shell, no
  OSS-vs-SaaS split, no separate `-core` engine.
- **Consumer isolation.** Each deployment brings its own
  Postgres / Redis / Stripe creds / API-key pepper. The shell never
  hardcodes credentials and never assumes shared state.
- **No default `RateCardResolver` impl.** OQ2 lock: per-product gateway
  always wires its own pricing.
- **Schema namespace `app.*`.** The shell owns `app.*`; consumer-owned
  schemas live alongside it.

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
- **The shell exposes interfaces; consuming services implement them.**
  `Wallet`, `AuthResolver`, `AdminAuthResolver`, `RateLimiter`,
  `RateCardResolver`, `StripeClient`.

## What lives elsewhere

- Chain-side payment: `payment-daemon/` (plans 0014, 0016).
- Multi-tenant operator console: `secure-orch-console/` (plan 0019).
