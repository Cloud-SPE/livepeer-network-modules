---
plan: 0013-shell
title: customer-portal shared SaaS shell library extraction — design
status: design-doc
phase: plan-only
opened: 2026-05-07
owner: harness
related:
  - "active plan 0013-openai — first consumer (collapses suite shell into openai-gateway/)"
  - "active plan 0013-vtuber — vtuber-gateway/ consumer"
  - "active plan 0013-video — video-gateway/ consumer"
  - "active plan 0013-runners — workload-runner migration (no consumer; foundation-independent)"
  - "completed plan 0009 — openai-gateway reference impl (protocol-only; no SaaS shell)"
  - "completed plan 0008 — gateway-adapters TypeScript middleware"
  - "design-doc docs/design-docs/migration-from-suite.md (cross-cutting digest)"
audience: rewrite contributors authoring SaaS-shell components
---

# Plan 0013-shell — customer-portal shared SaaS shell library (design)

> **Paper-only design brief.** No code, no `package.json`, no
> `pnpm-workspace.yaml` edits ship from this commit. Output: pinned
> decisions for the implementing agent across the openai-, vtuber-,
> and video-gateway components that consume the shell. Implementation
> is sequenced after a user walk locks each open question listed in
> §14.

## 1. Status and scope

Scope: **a shared SaaS-shell TypeScript library** at `customer-portal/`
in the rewrite monorepo, consumed by `openai-gateway/`,
`vtuber-gateway/`, and `video-gateway/`. The library extracts the
patterns the suite ships three times — once in
`livepeer-openai-gateway-core` + `livepeer-openai-gateway` shell, once
in `livepeer-vtuber-gateway`, once in `livepeer-video-gateway/apps/api`
— into one foundation that per-product gateways depend on.

Six concrete patterns ride on top:

1. API-key auth (`sk-{env}-{43-char-base64url}` plaintext, HMAC-SHA-256
   with operator-supplied pepper for at-rest hash).
2. Customer ledger (`balance_usd_cents` + `reserved_usd_cents` columns;
   `reservations` table keyed on `work_id`; reservation lifecycle
   open → committed/refunded).
3. Stripe integration (Checkout sessions for top-ups; idempotent
   webhook handler keyed on Stripe event id).
4. Lit + RxJS portal/admin SPAs (customer-facing portal + operator
   admin SPA; same component library + observable controllers).
5. Fastify pre-handler middleware composition (auth → rate-limit →
   handler; idempotency-key wrapper; rate-card hook).
6. drizzle schema for `customers / api_keys / reservations / topups /
   stripe_webhook_events / admin_audit_events / idempotency_requests`
   (all in an `app.*` Postgres schema).

Out of scope:

- Per-product workload routing, payment minting, mode dispatch — those
  belong to per-product gateways and to `gateway-adapters/` (plan 0008
  + 0008-followup).
- Stripe webhook *event semantics* per product — the shell handles
  Checkout-completed top-ups; product-specific webhooks (subscription
  events, metering connectors) belong to per-product gateways if they
  exist at all.
- Admin RBAC beyond a single-tenant operator-basic-auth resolver —
  multi-tenant operator console is `secure-orch-console/` territory
  (plan 0019).
- Any chain integration. Customer wallets are USD-cent ledgers; chain
  payment is the *gateway's* concern via `payment-daemon/`.

The shell ships as one workspace package, MIT-licensed, ESM-only,
TypeScript 5+, Node 20+, Fastify 5, Zod 3, drizzle-orm + `pg`,
ioredis, lit (frontend). It is **not** an OSS-vs-SaaS split — there
is no companion engine package, no published npm artifact, no
two-package coordination. Per-product gateways import from
`@livepeer-rewrite/customer-portal/*` via the pnpm workspace.

## 2. What predecessor work left unfinished

This brief replaces the approach embedded in the now-superseded
`0013-suite-openai-gateway-migration-brief.md` (moved to
`docs/exec-plans/superseded/0013-openai-pre-collapse.md`). That brief
proposed "Option A — collapse + separate SaaS repo" (see superseded
doc §3.5). The user walk on 2026-05-06 reversed the separate-repo
decision: a shared shell library inside the monorepo lets three
product gateways absorb their suite-side SaaS shells without paying
the dual-repo / lockstep-release tax three times.

The two-package split inside `livepeer-network-suite/` (the
`-core` engine + the suite shell, npm-published as
`@cloudspe/livepeer-openai-gateway-core@4.0.1`) is also unwound here:
the shell becomes a workspace library, not a separate distributable.

Plan 0008 (gateway-adapters TypeScript middleware) closed cleanly for
HTTP family modes; this brief is disjoint from plan 0008. Per-product
gateways consume `gateway-adapters/` for wire-protocol middleware
(`http-reqresp@v0`, `http-stream@v0`, `http-multipart@v0`) AND
`customer-portal/` for SaaS-shell middleware (auth, rate-limit,
ledger, Stripe). The two layers compose via Fastify pre-handlers.

## 3. Reference architecture

```
                    ┌───────────────────────────────────────────────┐
                    │  per-product gateway component                │
                    │  (openai-gateway / vtuber-gateway / video-gw) │
                    │                                               │
                    │   product-specific routes (chat, sessions,    │
                    │   live-streams, …)                            │
                    │           │                                   │
                    │           │  Fastify pre-handlers             │
                    │           ▼                                   │
                    │  ┌──────────────────┐                         │
                    │  │ customer-portal/ │   ← THIS BRIEF          │
                    │  │  - apiKeyAuth    │                         │
                    │  │  - rateLimiter   │                         │
                    │  │  - wallet        │                         │
                    │  │  - billing       │                         │
                    │  │  - portal/admin  │                         │
                    │  │  - stripe webhk  │                         │
                    │  │  - drizzle schema│                         │
                    │  │  - lit SPA libs  │                         │
                    │  └──────────────────┘                         │
                    │           │                                   │
                    │           │  composed with                    │
                    │           ▼                                   │
                    │  ┌──────────────────┐                         │
                    │  │ gateway-adapters │   ← plan 0008 / 0008-fu │
                    │  │  - mode driver   │                         │
                    │  │  - Livepeer-*    │                         │
                    │  │    headers       │                         │
                    │  └──────────────────┘                         │
                    │           │                                   │
                    └───────────┼───────────────────────────────────┘
                                │
                                ▼
                    ┌───────────────────────────────────────────────┐
                    │  capability-broker  (orch host)               │
                    └───────────────────────────────────────────────┘
                                ▲
                                │
                    ┌───────────────────────────────────────────────┐
                    │  payment-daemon (sender, gateway-side)        │
                    └───────────────────────────────────────────────┘
```

Two adjacent foundations (`customer-portal/` and `gateway-adapters/`)
sit under each per-product gateway. `customer-portal/` owns the
**customer-side** lifecycle (USD-denominated, Stripe-funded). 
`gateway-adapters/` owns the **wire** lifecycle (wei-denominated,
chain-funded via `payment-daemon`). Per-product gateways stitch them
into one Fastify app.

## 4. Component layout

`customer-portal/` is a single workspace package with multiple entry
points so consumers tree-shake unused subsystems.

```
customer-portal/
  AGENTS.md                    — entry-point map (links DESIGN.md, README.md)
  DESIGN.md                    — high-level shape; rationale for shared-shell choice
  README.md                    — consumer guide; "import these factories from these paths"
  Makefile                     — `make build`, `make test`, `make lint`, `make smoke`
  Dockerfile                   — N/A (library; no runtime image)
  compose.yaml                 — N/A (library; no compose stack)
  package.json                 — `@livepeer-rewrite/customer-portal`; ESM-only;
                                 exports map: ./auth, ./billing, ./db, ./stripe,
                                 ./middleware, ./repo, ./service, ./testing,
                                 ./frontend/portal, ./frontend/admin, ./frontend/shared
  tsconfig.json                — extends repo-root tsconfig.base.json
  vitest.config.ts             — node + jsdom envs (jsdom for Lit components)
  drizzle.config.ts            — points at ./src/db/schema.ts; output ./migrations
  migrations/                  — drizzle-kit-emitted SQL (numbered 0000..NNNN)
  src/
    index.ts                   — barrel for the public API (re-export from subpackages)
    auth/
      apiKey.ts                — generate/hash/verify (HMAC-SHA-256 + pepper)
      apiKeyAuthResolver.ts    — Fastify-agnostic AuthResolver impl
      adminBasicAuth.ts        — operator-basic-auth resolver
      sessionBearer.ts         — short-lived child bearer for product-scoped sessions
    billing/
      wallet.ts                — Wallet impl: reserve / commit / refund over (balance, reserved)
      reservations.ts          — workId-keyed reservation CRUD
      topups.ts                — top-up record CRUD; stripe-session linkage
    stripe/
      checkout.ts              — Checkout session factory
      webhook.ts               — Fastify route + idempotent event handling
      provider.ts              — Stripe SDK provider interface (testable)
    middleware/
      authPreHandler.ts        — Fastify hook: resolve API key → caller principal
      rateLimitPreHandler.ts   — Fastify hook: bucket-based RPS via Redis
      idempotency.ts           — Fastify hook + on-send: idempotency-key dedup
      errors.ts                — toHttpError (maps shell errors to RFC 7807 bodies)
    db/
      schema.ts                — drizzle pgSchema('app') tables (see §7)
      migrate.ts               — runMigrations(db, dir) helper
      pool.ts                  — pg + drizzle factory; transaction helper
    repo/
      customers.ts             — insertCustomer / findById / selectForUpdate / etc.
      apiKeys.ts               — insert / findByHash / revoke
      reservations.ts          — open / commit / refund
      topups.ts                — insert / updateStatus
      stripeWebhookEvents.ts   — recordEvent (idempotent on event_id)
      adminAuditEvents.ts      — append (no update; immutable log)
      idempotencyRequests.ts   — find/insert/complete
    service/
      authenticate.ts          — match plaintext key → caller; cache hit (Redis)
      pricing.ts               — RateCardResolver interface (resolve(capability, offering))
      admin/                   — admin engine façade (operator queries; suspends; refunds)
        engine.ts
    config/
      env.ts                   — Zod schemas: PG_*, REDIS_*, STRIPE_*, API_KEY_PEPPER, …
      types.ts                 — exported config interfaces
    testing/
      pgTestPool.ts            — Testcontainers Postgres factory (vitest fixture)
      redisTestClient.ts       — embedded Redis or testcontainer client
      stripeMock.ts            — Stripe SDK stub with deterministic event corpus
      walletFakes.ts           — in-memory Wallet for product-gateway unit tests
    frontend/                  — bundled per-frontend; pnpm-managed sub-workspace
      portal/                  — customer-facing SPA component library
        components/            — Lit elements (login / dashboard / keys / billing / settings)
        controllers/           — RxJS observable controllers
      admin/                   — operator SPA component library
        components/            — Lit elements (login / customers / topups / audit / nodes / pricing)
      shared/                  — shared design tokens + base components
        components/            — bridge-button / bridge-dialog / bridge-table / bridge-toast
        controllers/           — observable-controller wiring lit ReactiveController to RxJS
        css/                   — base.css / reset.css / tokens.css / utilities.css
        lib/                   — api-base.js (fetch wrapper) / route.js / session-storage.js
  test/
    integration/               — Postgres + Redis-backed integration tests
    smoke/                     — minimal Fastify-app smoke validating wiring
```

Per-product gateways depend on the package via `pnpm` workspace; they
import what they need (`from '@livepeer-rewrite/customer-portal/auth'`)
and compose the rest.

## 5. Source-to-destination file map

The library extracts from three suite shells. Lines cited are at the
`v4.0.1` submodule pin recorded in `docs/design-docs/migration-from-suite.md`.

### 5.1 Auth + API keys

| Source | Target |
|---|---|
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/service/auth/keys.ts:1-49` (generate/hash/verify) | `customer-portal/src/auth/apiKey.ts` |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/service/auth/cache.ts` (Redis hit cache for resolved keys) | `customer-portal/src/service/authenticate.ts` (cache subsystem) |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/service/auth/authenticate.ts` (entry-point pattern) | `customer-portal/src/service/authenticate.ts` |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/service/auth/authResolver.ts` (Fastify-agnostic resolver) | `customer-portal/src/auth/apiKeyAuthResolver.ts` |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/service/admin/basicAuthResolver.ts:1-19` (operator basic-auth) | `customer-portal/src/auth/adminBasicAuth.ts` |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/service/auth/sessionBearer.ts` (child bearer pattern) | `customer-portal/src/auth/sessionBearer.ts` |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/service/auth/workerControlBearer.ts` (worker-control bearer) | **out of scope** (vtuber-specific; lives in `vtuber-gateway/`) |

### 5.2 Wallet / billing / reservations

| Source | Target |
|---|---|
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/service/billing/wallet.ts` (`createPrepaidQuotaWallet`) | `customer-portal/src/billing/wallet.ts` |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/service/billing/reservations.ts` | `customer-portal/src/billing/reservations.ts` |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/service/billing/topups.ts` | `customer-portal/src/billing/topups.ts` |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/repo/customers.ts:8-50` (insertCustomer / findById / selectForUpdate / updateBalanceFields / incrementBalance / search) | `customer-portal/src/repo/customers.ts` |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/repo/apiKeys.ts` | `customer-portal/src/repo/apiKeys.ts` |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/repo/reservations.ts` | `customer-portal/src/repo/reservations.ts` |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/repo/topups.ts` | `customer-portal/src/repo/topups.ts` |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/service/billing/inMemoryWallet.ts` (in-memory variant for tests) | `customer-portal/src/testing/walletFakes.ts` |

### 5.3 Stripe

| Source | Target |
|---|---|
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/runtime/http/billing/topup.ts` (Checkout session creation route) | `customer-portal/src/stripe/checkout.ts` (helper) + per-product gateway wires the route |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/runtime/http/stripe/webhook.ts` | `customer-portal/src/stripe/webhook.ts` |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/providers/stripe/sdk.ts` (`createSdkStripeClient`) | `customer-portal/src/stripe/provider.ts` |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/repo/stripeWebhookEvents.ts` | `customer-portal/src/repo/stripeWebhookEvents.ts` |

### 5.4 Middleware

| Source | Target |
|---|---|
| `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/http/middleware/auth.ts` (authPreHandler) | `customer-portal/src/middleware/authPreHandler.ts` |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/http/middleware/rateLimit.ts` | `customer-portal/src/middleware/rateLimitPreHandler.ts` |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/runtime/http/middleware/idempotency.ts` (`idempotencyOnSend`, `idempotencyPreHandler`) | `customer-portal/src/middleware/idempotency.ts` |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/http/errors.ts` (`toHttpError`) | `customer-portal/src/middleware/errors.ts` |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/service/rateLimit/slidingWindow.ts` | `customer-portal/src/service/rateLimit.ts` |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/service/rateLimit/concurrency.ts` | `customer-portal/src/service/rateLimit.ts` |

### 5.5 Schema

| Source | Target |
|---|---|
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/repo/schema.ts:14-123` (customers / apiKeys / reservations / topups / stripeWebhookEvents / adminAuditEvents enum + tables) | `customer-portal/src/db/schema.ts` |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/repo/schema.ts:125-154` (idempotencyRequests + enums) | `customer-portal/src/db/schema.ts` |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/migrations/0000_app_init.sql` (initial schema) | `customer-portal/migrations/0000_app_init.sql` |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/migrations/0003_idempotency_requests.sql` | `customer-portal/migrations/0001_idempotency_requests.sql` (renumbered) |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/migrations/0001_rate_card.sql` + `0002_seed_rate_card.sql` + `0004_retail_pricing.sql` (rate-card tables) | **NOT migrated to shell** — pricing schema is product-specific and lives in per-product gateway migrations. The shell exposes a `RateCardResolver` interface only. See §9.

### 5.6 Frontend (Lit + RxJS)

| Source | Target |
|---|---|
| `livepeer-network-suite/livepeer-openai-gateway/frontend/shared/components/*.js` (8 bridge-* Lit elements) | `customer-portal/src/frontend/shared/components/*.ts` (TS port) |
| `livepeer-network-suite/livepeer-openai-gateway/frontend/shared/controllers/observable-controller.js` | `customer-portal/src/frontend/shared/controllers/observable-controller.ts` |
| `livepeer-network-suite/livepeer-openai-gateway/frontend/shared/css/{base,reset,tokens,utilities}.css` | `customer-portal/src/frontend/shared/css/` (verbatim) |
| `livepeer-network-suite/livepeer-openai-gateway/frontend/shared/lib/{api-base,events,route,session-storage,validators}.js` | `customer-portal/src/frontend/shared/lib/*.ts` (TS port) |
| `livepeer-network-suite/livepeer-openai-gateway/frontend/portal/components/portal-{app,login,dashboard,keys,usage,billing,settings}.js` (7 portal components) | `customer-portal/src/frontend/portal/components/portal-*.ts` |
| `livepeer-network-suite/livepeer-openai-gateway/frontend/admin/components/admin-{app,login,health,customers,customer-detail,topups,audit,nodes,node-detail,reservations,config,api-keys,projects,pricing,rate-card-{chat,embeddings,images,speech,transcriptions},retail-pricing-capability}.js` | `customer-portal/src/frontend/admin/components/admin-*.ts`. Rate-card-* components are product-specific and **stay in `openai-gateway/`** under `frontend/admin/`. The shell admin SPA wires only the cross-product surface (auth / customers / topups / audit / nodes / reservations). |

The frontend is **TypeScript-ported** during the migration — the suite
currently ships .js Lit components; the rewrite uses TS Lit
consistently.

### 5.7 Operator basic-auth admin engine

| Source | Target |
|---|---|
| `livepeer-network-suite/livepeer-openai-gateway-core/src/service/admin/engine.ts` (admin façade interface) | `customer-portal/src/service/admin/engine.ts` |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/runtime/http/admin/routes.ts` (admin REST routes) | `customer-portal/` ships the engine + a route-factory; per-product gateway calls `registerAdminRoutes(app, engine)` |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/repo/adminAuditEvents.ts` | `customer-portal/src/repo/adminAuditEvents.ts` |

## 6. Tech-stack lock + variance justification

**Canonical stack — no variance for this brief.**

- Node 20+ runtime; ESM-only (`"type": "module"` in `package.json`).
- TypeScript 5+; `tsconfig.json` extends repo-root `tsconfig.base.json`
  with `module: "ES2022"` + `moduleResolution: "Bundler"`.
- Fastify 5 for HTTP plumbing. The suite is on Fastify 4
  (`livepeer-openai-gateway-core/src/runtime/http/audio/transcriptions.ts:40`
  registers via Fastify 4 plugin shape); migrating to 5 is a small
  shift in route-options typing. Consumers register routes via the
  shell's factories so the framework version is hidden.
- Zod 3 (already pinned across the rewrite + suite; no version
  contention).
- drizzle-orm + `pg` for Postgres; ioredis for Redis.
- Lit 3 + RxJS 7 for the frontend. The suite ships Lit 3 already
  (`frontend/portal/package.json:dependencies`); RxJS 7 is also the
  suite pin.
- pnpm workspace (matches the rewrite root). The suite shell is npm
  workspaces (`livepeer-openai-gateway/package.json`); the rewrite
  retires that in favour of pnpm via a clean-slate copy.
- Postgres 16+ runtime; same as suite (`compose.yaml:image:
  postgres:16-alpine`).
- Stripe SDK pin: same major as suite (`stripe@14`); confirmed
  compatible with Stripe API version `2024-06-20`. No bump as part of
  this migration.

**No variance allowed in this brief.** Per-product gateways with
justified variance (e.g. vtuber-pipeline Python; avatar-renderer
browser-side TS) carry their own variance notes in their own briefs;
those components do **not** consume `customer-portal/`. The shell is
strictly Node + browser TS.

## 7. DB schema

`customer-portal/` owns the `app.*` Postgres schema. drizzle migration
files emit to `customer-portal/migrations/`. Per-product gateways
declare their own `media.*` (video) / `vtuber.*` (vtuber) /
`openai.*` (openai usage rollups) namespaces and run **their own**
migrations against the same database via the shell's `migrate.ts`
helper.

| Table | Purpose | Source |
|---|---|---|
| `app.customers` | One row per customer; tier (`free` / `prepaid`); status; balance + reserved cents; quota fields. | suite shell `repo/schema.ts:29-42` |
| `app.api_keys` | One row per issued key; `hash` (HMAC-SHA-256(pepper, plaintext)); FK customer_id; revocation timestamp. | suite shell `repo/schema.ts:44-61` |
| `app.reservations` | One row per workId; `kind` (`prepaid` / `free`); amount cents OR amount tokens; state (`open` / `committed` / `refunded`). | suite shell `repo/schema.ts:63-81` |
| `app.topups` | One row per Stripe Checkout session; status (`pending` / `succeeded` / `failed` / `refunded`). | suite shell `repo/schema.ts:83-100` |
| `app.stripe_webhook_events` | One row per Stripe event id; replay protection. | suite shell `repo/schema.ts:102-107` |
| `app.admin_audit_events` | Append-only operator action log. | suite shell `repo/schema.ts:109-123` |
| `app.idempotency_requests` | Per-customer idempotency-key dedup with cached response. | suite shell `repo/schema.ts:128-154` |

**Out of scope for this shell schema:**

- `app.rate_card_*` tables — per-product pricing is per-product
  schema. The shell exposes `RateCardResolver` as an *interface*; the
  openai-gateway brings its own rate-card tables (and migrations). See
  §9.
- `app.retail_price_*` tables — same reasoning; per-product gateway
  owns the table set.

Migrations are linear (no branching) and emitted by `drizzle-kit
generate`. Filename convention: `0000_app_init.sql`,
`0001_idempotency_requests.sql`, `0002_<descriptive_slug>.sql`.

`runMigrations(db, migrationsDir)` is a thin wrapper around drizzle's
`migrate` helper, using a `_app_migrations` advisory lock to make
concurrent boots safe.

## 8. Customer-facing surfaces

### 8.1 UI flows

| Flow | Component | Source |
|---|---|---|
| Customer login (email + password) | `portal-login` | suite `frontend/portal/components/portal-login.js` |
| Customer dashboard (balance, recent usage) | `portal-dashboard` | suite `portal-dashboard.js` |
| API-key list + reveal-once-on-issue | `portal-keys` | suite `portal-keys.js` |
| Top-up via Stripe Checkout (redirect) | `portal-billing` | suite `portal-billing.js` |
| Usage history (per-day rollups) | `portal-usage` | suite `portal-usage.js` (the data shape is product-specific; the *component shell* renders whatever rows the product gateway returns) |
| Account settings (email change) | `portal-settings` | suite `portal-settings.js` |

The `portal-app.js` shell component is provided by the library; per-
product gateways serve a `portal/index.html` that loads the shell +
their product-specific extra components if any.

### 8.2 API endpoints (shell-owned)

| Method + path | Purpose |
|---|---|
| `POST /v1/account/register` | Customer self-serve register (email + password, email-confirm later). |
| `POST /v1/account/login` | Issue session cookie. |
| `POST /v1/account/logout` | Clear session. |
| `GET /v1/account/me` | Return current customer principal + balance. |
| `POST /v1/account/api-keys` | Issue new key (returns plaintext once). |
| `GET /v1/account/api-keys` | List keys (no plaintext; only label + last-used). |
| `POST /v1/account/api-keys/:id/revoke` | Mark a key revoked. |
| `POST /v1/billing/topup/checkout` | Create Stripe Checkout session; return `redirect_url`. |
| `POST /v1/stripe/webhook` | Receive Stripe events; replay-protected on `event.id`. |
| `GET /v1/admin/health` | Operator health probe. |
| `GET /v1/admin/customers` | Operator customer list / search. |
| `POST /v1/admin/customers/:id/suspend` | Operator suspend customer. |
| `POST /v1/admin/customers/:id/unsuspend` | Operator unsuspend. |
| `POST /v1/admin/topups/:id/refund` | Operator manual refund. |
| `GET /v1/admin/audit` | Operator action log. |

Per-product gateway adds product-specific routes (chat completions,
session-open, live-stream-start, …). The `Wallet` interface is the
contract: product gateway calls `wallet.reserve(callerId, costQuote)`
on entry, `wallet.commit(handle, usage)` on success, `wallet.refund`
on failure. Reservations are keyed on `workId` so the broker's
session-open phase reconciles cleanly.

### 8.3 OAuth / external auth

The suite shell does **not** ship OAuth. The shell ships email +
password + cookie sessions. Operators wanting Google / GitHub OAuth
add it as a per-product gateway concern (or a future shell expansion
behind `AuthResolver`). Out of scope here.

### 8.4 Egress workers / chat workers

Not in scope. Workload egress (e.g. RTMP origin for video, vtuber
control-WS) lives in per-product gateways. The shell owns the
customer-side lifecycle only.

## 9. Cross-component dependencies

The shell exposes five **interfaces** that per-product gateways
consume + occasionally extend:

| Interface | Where defined | Consumers |
|---|---|---|
| `Wallet` | `customer-portal/src/billing/wallet.ts` | openai-gateway, vtuber-gateway, video-gateway |
| `AuthResolver` | `customer-portal/src/auth/apiKeyAuthResolver.ts` | all three product gateways; Fastify pre-handler decorator |
| `AdminAuthResolver` | `customer-portal/src/auth/adminBasicAuth.ts` | all three product gateways |
| `RateLimiter` | `customer-portal/src/middleware/rateLimitPreHandler.ts` | all three product gateways |
| `RateCardResolver` | `customer-portal/src/service/pricing.ts` | per-product gateway implements (their own pricing tables) |
| `BillingProvider` | `customer-portal/src/stripe/provider.ts` | provider-shaped — shell ships the Stripe impl; product gateways could ship a different impl if they don't bill via Stripe (none do today) |
| `RegistryClient` | **NOT shell-owned** — lives in `gateway-adapters/` per plan 0008-followup. Listed here for completeness because every consumer needs both the shell's `Wallet` AND `gateway-adapters/`'s `RegistryClient`. |

Per-product gateway composition example (openai-gateway):

```typescript
// per-product gateway src/main.ts
import { createPgDatabase, runMigrations } from '@livepeer-rewrite/customer-portal/db';
import { createApiKeyAuthResolver, authPreHandler } from '@livepeer-rewrite/customer-portal/middleware';
import { createPrepaidQuotaWallet } from '@livepeer-rewrite/customer-portal/billing';
import { createRateLimiter, rateLimitPreHandler } from '@livepeer-rewrite/customer-portal/middleware';
import { registerStripeWebhookRoute } from '@livepeer-rewrite/customer-portal/stripe';
import { createPaymentSender } from '@livepeer-rewrite/gateway-adapters/payment';
import { httpReqRespSend } from '@livepeer-rewrite/gateway-adapters/modes';
import { registerChatCompletionsRoute } from './routes/chatCompletions.js';
// …compose Fastify app, drop in pre-handlers, register product routes…
```

## 10. Configuration surface

Shell-owned env vars (Zod-validated at boot via `customer-portal/src/config/env.ts`):

| Env var | Required | Purpose |
|---|---|---|
| `PG_HOST`, `PG_PORT`, `PG_USER`, `PG_PASSWORD`, `PG_DATABASE` | yes | Postgres connection. |
| `REDIS_URL` | yes | Redis (rate-limit buckets + auth-cache). |
| `API_KEY_PEPPER` | yes | HMAC-SHA-256 pepper for at-rest API-key hashes. Operator MUST rotate at boot only — rotating a live key invalidates every key. |
| `API_KEY_ENV_PREFIX` | no (default `live`) | `live` or `test` prefix in `sk-{prefix}-{43}`. |
| `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `STRIPE_API_VERSION` | yes (if billing enabled) | Stripe SDK config. |
| `STRIPE_TOPUP_SUCCESS_URL`, `STRIPE_TOPUP_CANCEL_URL` | yes (if billing enabled) | Checkout redirect URLs. |
| `ADMIN_BASIC_AUTH_USER`, `ADMIN_BASIC_AUTH_PASS` | yes | Operator basic-auth credentials. Shell mounts `/v1/admin/*` behind this. |
| `RATE_LIMIT_TIER_DEFAULT` | no (default 60 req/min) | Default rate-limit tier ceiling. |
| `RATE_LIMIT_BURST` | no (default 10) | Sliding-window burst. |
| `IDEMPOTENCY_TTL_SECONDS` | no (default 86400) | Idempotency-key cache TTL. |
| `SESSION_COOKIE_SECRET` | yes | HMAC secret for the portal session cookie. |
| `BILLING_FREE_TIER_QUOTA_TOKENS` | no (default 0) | Monthly free-tier token grant; 0 disables free tier. |

Product-gateway-owned env vars are documented in the per-product
brief (e.g. `LIVEPEER_BROKER_URL`, `LIVEPEER_PAYER_DAEMON_SOCKET`,
`LIVEPEER_PAYER_DEFAULT_FACE_VALUE_WEI`).

YAML config: the shell does **not** ship YAML. Per-product gateways
that want YAML (e.g. for rate-card overrides) own that file at the
product-gateway level. Suite's `service-registry-config.example.yaml`
is product-side; the shell never reads it.

## 11. Conformance / smoke tests

The shell ships **integration smoke tests** because it has no wire
protocol of its own (all wire-spec smokes live with `gateway-adapters/`
and the per-product gateways). Layout:

- `customer-portal/test/integration/wallet.test.ts` — Postgres-backed
  wallet reserve / commit / refund correctness; concurrent reservation
  handling (FOR UPDATE lock).
- `customer-portal/test/integration/apiKey.test.ts` — issue / hash /
  verify; revocation; cache hit/miss.
- `customer-portal/test/integration/stripeWebhook.test.ts` — replay
  protection; Checkout-completed → `topups.status = succeeded`; failed
  payment intent → `topups.status = failed`.
- `customer-portal/test/integration/idempotency.test.ts` — same key
  twice returns cached body; different key after TTL works.
- `customer-portal/test/integration/rateLimit.test.ts` — burst budget;
  per-customer-tier ceilings.
- `customer-portal/test/smoke/fastify-wiring.test.ts` — minimal
  consumer Fastify app; pre-handlers fire in correct order; auth →
  rate-limit → handler.

Per-product gateway smokes (in their own briefs) layer on the shell
smokes; the shell smokes do not exercise wire protocol or
`payment-daemon`.

CI integration via Testcontainers (Postgres + Redis); same pattern the
suite uses (`livepeer-openai-gateway/packages/livepeer-openai-gateway/src/service/billing/testPg.ts`).

## 12. Operator runbook deltas

Library, not a runtime — the shell does not ship its own runbook. It
contributes the following **deltas** to consumer runbooks:

1. **API-key pepper rotation.** The pepper is per-deployment; rotating
   it invalidates every issued key. Document the procedure: stand up a
   new instance with the new pepper; mass-issue replacement keys via
   the admin console; deprecate old keys with a grace window;
   eventually revoke all old-pepper keys. (Lifted from suite
   operator-deployment runbook; copy verbatim into per-product gateway
   runbooks.)
2. **Stripe webhook signing-secret pin.** Document that
   `STRIPE_WEBHOOK_SECRET` must match the Stripe Dashboard's webhook
   endpoint config. Mismatch = silent 4xx.
3. **DB migration cadence.** Shell migrations run **before** product
   migrations on boot. Per-product gateway runbook documents which
   migrations come first; the shell's `runMigrations` helper takes a
   directory so the order is explicit.
4. **Customer ledger reconciliation.** When a customer's `balance` +
   `reserved` diverges from `topups.amount_succeeded - committed
   reservations`, the operator runs an ops query. Document the
   reconciliation SQL in the shell's `docs/operator-ledger.md`; per-
   product runbook references it.
5. **Idempotency-key tail rows.** `idempotency_requests` grows
   unbounded by default; the shell ships a `pg_cron`-shaped vacuum
   query at `customer-portal/scripts/idempotency-vacuum.sql`.
6. **Free-tier quota reset.** A daily cron resets
   `customers.quota_tokens_remaining` to
   `customers.quota_monthly_allowance` if `quota_reset_at` < now.
   Shell ships the SQL; operator schedules the cron.

## 13. Migration sequence

5 phases; each independently revertable. Per-product gateway briefs
gate on phase 5 of this brief landing.

### Phase 1 — Package scaffold + DB migrations

Create `customer-portal/` workspace package shell (no logic; just
`package.json`, `tsconfig.json`, drizzle config, dirs). Land the
schema (`src/db/schema.ts` + `migrations/0000_app_init.sql`) byte-
identical to the suite's `0000_app_init.sql` modulo the rate-card
tables (which stay in the openai-gateway brief).

**Acceptance:** `pnpm -F @livepeer-rewrite/customer-portal build`
green; `drizzle-kit migrate` runs cleanly against an empty Postgres.

### Phase 2 — Auth + billing services

Port `auth/` + `billing/` + `repo/` from the suite shell. Wire
`createPrepaidQuotaWallet`, `issueKey`, `verifyApiKey`. Add
integration tests under `test/integration/`.

**Acceptance:** integration tests green; in-memory wallet fakes work
for unit tests. Diff: ~1,500 LOC ported.

### Phase 3 — Stripe + webhook

Port `stripe/checkout.ts` + `stripe/webhook.ts` + replay-protection.
Wire SDK provider interface for testability.

**Acceptance:** Stripe smoke against test-mode keys; webhook replay
protection holds under concurrent fires.

### Phase 4 — Middleware + admin engine + Fastify wiring helpers

Port `authPreHandler`, `rateLimitPreHandler`, `idempotency` middleware;
admin engine façade; `errors.ts` (`toHttpError`); route-factories
(`registerStripeWebhookRoute(app, deps)`, `registerAccountRoutes`,
`registerAdminRoutes`).

**Acceptance:** smoke Fastify app stitches the pieces; pre-handlers
fire in canonical order; admin SPA loads against the engine.

### Phase 5 — Frontend (Lit + RxJS) port

TS-port the suite's `frontend/shared/`, `frontend/portal/`,
`frontend/admin/` directories into `src/frontend/`. Land the
`portal-app` + `admin-app` shells. Per-product gateways will skin
these with their own components (e.g. openai-gateway adds
`admin-rate-card-chat`).

**Acceptance:** `pnpm -F @livepeer-rewrite/customer-portal build`
emits frontend bundles. A consumer gateway can import and render the
portal shell.

After phase 5, the per-product gateway briefs (`0013-openai`,
`0013-vtuber`, `0013-video`) are unblocked. They consume the shell
package + add their product surface.

## 14. Resolved decisions

User walk-through 2026-05-06 locked the high-level decisions; the
following are recorded as `DECIDED` blocks for the implementing agent.

### Q1. Single shared shell vs three independent SaaS shells

**DECIDED: single shared shell at `customer-portal/`.** The suite
ships three independent shells today (openai, vtuber, video); they
duplicate API-key, ledger, Stripe, portal/admin. One workspace library
inside the rewrite monorepo lets all three product gateways consume
the same factories. The earlier-superseded brief proposed a separate
SaaS repo (option A); the user reversed that on 2026-05-06.

### Q2. OSS-vs-SaaS engine/shell split

**DECIDED: collapse.** No `-core` engine published to npm; no separate
shell. One workspace package, one CI lane, no lockstep release. The
suite's `@cloudspe/livepeer-openai-gateway-core@4.0.1` is unpublished
or `npm deprecate`-marked at the openai-gateway phase-4 cut.

### Q3. Tech-stack version targets

**DECIDED: Fastify 5 + Zod 3 + drizzle-orm + pnpm workspace + Node
20+ + TypeScript 5 + Lit 3 + RxJS 7 + Postgres 16 + Stripe SDK 14.**
No variance allowed in this brief; per-product variance lives in
those briefs. Confirmed compatible with rewrite root tsconfig +
package.json.

### Q4. License

**DECIDED: MIT** across the entire monorepo, this library included.
The suite's `livepeer-openai-gateway` shell does not carry a public
LICENSE file (internal-only); the rewrite is MIT from day one.

### Q5. Frontend port: keep .js or move to .ts

**DECIDED: TypeScript port.** The suite ships .js Lit components
(`frontend/portal/components/portal-app.js` etc.); the rewrite ships
.ts uniformly. The port is mechanical (Lit @customElement decorators
+ Reactive properties translate cleanly). Bundling is ESM via tsc +
the consumer's bundler (Vite for portal/admin SPAs).

### Q6. Schema namespace ownership

**DECIDED: shell owns `app.*`; per-product owns `<product>.*`.**
Shell migrations land in `customer-portal/migrations/`; per-product
gateway migrations land in `<product>-gateway/migrations/`. Boot
order: shell first, product after, both via the shell's
`runMigrations(db, dir)` helper. The product schemas may FK into
`app.customers` (no FK from `app.*` into product schemas — keeps the
shell self-contained).

### Q7. Rate-card tables — shell or product?

**DECIDED: per-product.** The suite mixes rate-card tables into the
shell's schema (`livepeer-openai-gateway/migrations/0001_rate_card.sql`,
`0004_retail_pricing.sql`); pricing semantics are product-specific
(chat tokens / image rendering / live-streaming minutes / vtuber
session-seconds). The shell exposes a `RateCardResolver` interface
only; per-product gateway implements + owns the tables.

### Q8. Free-tier semantics

**DECIDED: shell ships the schema fields (`quota_tokens_remaining`,
`quota_monthly_allowance`, `quota_reserved_tokens`, `quota_reset_at`)
+ a default cron-resettable monthly grant.** Per-product can override
the units (tokens vs minutes vs seconds) by computing into the same
column. The numeric column stays product-neutral; the **meaning** of
the unit is product-defined.

### Q9. Admin console multi-tenancy

**DECIDED: single-tenant operator basic-auth in this shell.**
Multi-tenant operator views / RBAC are `secure-orch-console/`
territory (plan 0019). Shell ships only basic-auth; one operator
identity per deployment.

### Open questions surfaced for the user walk

- **OQ1.** Should `customer-portal/src/frontend/` ship as a separate
  workspace package (sibling to the Node lib) so the SPA bundle
  resolution is cleaner? The suite uses a sibling
  `frontend/{portal,admin,shared}` workspace under
  `livepeer-openai-gateway/`. Recommendation: same shape — one
  pnpm sub-workspace under `customer-portal/frontend/` with its own
  `package.json`. **Surface for user lock.**
- **OQ2.** Should the shell ship a default `RateCardResolver` impl
  that reads from a YAML file (no DB), so very-small operators can
  skip Postgres rate-card tables? Suite does not. Recommendation: no;
  per-product gateway always wires its own. **Surface for user lock.**
- **OQ3.** API-key pepper: shell-only or per-product? If a customer
  is shared across products, a single pepper is required. If each
  product has its own customer set, each product can have its own
  pepper. Recommendation: shared pepper (one customer scope across
  products); document per-product customer scoping as a future flag.
  **Surface for user lock.**
- **OQ4.** Frontend bundling: tsc + bundler (Vite) at consumer side,
  or pre-bundled rollup output ships with the shell? Suite pre-bundles
  via the shell's Dockerfile build stage. Recommendation: ship .ts
  sources + a `shared/dist/` pre-bundled artifact for the frontend
  shared library; per-product Vite bundles its own portal/admin SPA
  with the shared dist as an ESM dep. **Surface for user lock.**

## 15. Out of scope (forwarding addresses)

- **Per-product workload routing, payment minting, mode dispatch** —
  forwarded to `gateway-adapters/` (plans 0008, 0008-followup) and
  per-product briefs (`0013-openai`, `0013-vtuber`, `0013-video`).
- **Chain-side payment lifecycle (sender daemon)** — `payment-daemon/`
  (plans 0014, 0016) owns this. The shell does NOT call `payment-
  daemon` directly; the per-product gateway does, after the shell's
  `Wallet.reserve` returns success.
- **Multi-tenant operator console / RBAC** — `secure-orch-console/`
  (plan 0019).
- **OAuth providers / SSO** — future shell expansion, not in v0.1.
- **WebAuthn / passkeys** — future; v0.1 is email + password.
- **Sub-second rate limiting** — Redis sliding-window; sub-second is
  a future enhancement.
- **Customer-side webhooks (egress to customer URL)** — future. v0.1
  does not push events to customer endpoints.
- **Multi-currency** — USD only. v0.1 ledger is USD cents end-to-end;
  Stripe top-ups in USD.
- **Refunds-from-customer-portal UX** — v0.1 admin-only refund;
  customer-initiated refund is a future enhancement.
- **`livepeer-byoc/register-capabilities/`** — out of scope for the
  rewrite per plan 0018; broker scrapes `/registry/offerings`
  server-side. Not relevant to the shell.
- **`livepeer-modules-project/`** — left alone; user retires manually
  per `docs/design-docs/migration-from-suite.md` §4.

---

## Appendix A — file paths cited

This monorepo:

- `docs/exec-plans/superseded/0013-openai-pre-collapse.md` (the
  original plan 0013; supersedes the separate-SaaS-repo proposal).
- `docs/design-docs/migration-from-suite.md` (cross-cutting digest;
  refreshed by this brief).
- `docs/exec-plans/active/0013-openai-gateway-collapse.md` (consumer).
- `docs/exec-plans/active/0013-vtuber-suite-migration.md` (consumer).
- `docs/exec-plans/active/0013-video-gateway-migration.md` (consumer).
- `docs/exec-plans/completed/0008-gateway-adapters-typescript-middleware.md`
  (parallel foundation).
- `docs/exec-plans/active/0008-followup-gateway-adapters-non-http-modes.md`
  (extends `gateway-adapters/`).
- `docs/exec-plans/completed/0009-openai-gateway-reference.md` (the
  reference impl that this shell extends with SaaS surfaces).

Suite + reference paths (citation only; no port):

- `livepeer-network-suite/livepeer-openai-gateway-core/src/interfaces/index.ts:9-22`
  (engine adapter contracts; Wallet / AuthResolver / RateLimiter; the
  shape this shell preserves).
- `livepeer-network-suite/livepeer-openai-gateway-core/src/service/admin/basicAuthResolver.ts:1-19`
  (admin basic-auth; ported).
- `livepeer-network-suite/livepeer-openai-gateway-core/src/service/billing/inMemoryWallet.ts`
  (in-memory wallet for tests; ported into `testing/walletFakes.ts`).
- `livepeer-network-suite/livepeer-openai-gateway-core/src/service/rateLimit/`
  (sliding-window + concurrency; ported).
- `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/main.ts:32-64`
  (composition root showing the layered import pattern; rewrite
  consumers replicate the shape).
- `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/repo/schema.ts:14-154`
  (drizzle schema; ported except `rate_card_*` and `retail_price_*`).
- `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/service/auth/keys.ts:1-49`
  (API-key generate/hash/verify; ported verbatim).
- `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/service/billing/wallet.ts`
  (`createPrepaidQuotaWallet`; ported).
- `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/runtime/http/stripe/webhook.ts`
  (Stripe webhook; ported).
- `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/runtime/http/billing/topup.ts`
  (Checkout factory; ported).
- `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/migrations/0000_app_init.sql`
  (initial app schema; ported).
- `livepeer-network-suite/livepeer-openai-gateway/frontend/{shared,portal,admin}/`
  (Lit/RxJS frontend; TS-ported).
- `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/http/middleware/{auth,rateLimit}.ts`
  (Fastify pre-handler factory pattern; ported).
- `livepeer-network-suite/livepeer-vtuber-gateway/src/service/auth/sessionBearer.ts`
  (child-bearer for session-scoped auth; ported).
- `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/http/errors.ts`
  (`toHttpError`; ported).
