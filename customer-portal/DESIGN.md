# customer-portal — design

The full design lives in the plan brief:
[`../docs/exec-plans/active/0013-shell-customer-portal-extraction.md`](../docs/exec-plans/active/0013-shell-customer-portal-extraction.md).

This file pins the per-component invariants the implementing agent must
preserve when extending the shell.

## Invariants

1. **Single workspace package.** Q1 + Q2 locks. No OSS-vs-SaaS split, no
   separate engine. One `package.json`, one CI lane.
2. **Per-product isolation.** OQ3 + FQ1 + FQ3 locks. Each per-product
   gateway brings its own Postgres, Redis, Stripe credentials, and
   API-key pepper. The shell never hardcodes them and never assumes
   shared state across gateways.
3. **Schema namespace `app.*`.** Q6 lock. Shell migrations create
   `app.customers`, `app.api_keys`, `app.reservations`, `app.topups`,
   `app.stripe_webhook_events`, `app.admin_audit_events`,
   `app.idempotency_requests`. Per-product migrations live alongside in
   their own namespace (`openai.*`, `vtuber.*`, `media.*`).
4. **Rate-card tables stay in per-product migrations.** Q7 + OQ2 locks.
   The shell exposes `RateCardResolver` as an interface only; per-product
   gateway owns the table set and the resolver impl.
5. **Free-tier semantics ride on shared columns.** Q8 lock. Shell ships
   `quota_tokens_remaining`, `quota_monthly_allowance`,
   `quota_reserved_tokens`, `quota_reset_at`. Per-product can override
   the unit (tokens vs minutes vs seconds).
6. **API-key prefix `sk-{env}-{rand43}`.** FQ2 lock. No product slug;
   product is implicit in which gateway's pepper authenticates.
7. **Stripe creds as config.** FQ3 lock. `STRIPE_SECRET_KEY`,
   `STRIPE_WEBHOOK_SECRET`, `STRIPE_PUBLISHABLE_KEY` are env-only.
8. **Admin SPA single-tenant.** Q9 lock. Basic-auth via
   `ADMIN_USERNAME` + `ADMIN_PASSWORD_HASH`. Multi-tenant operator
   console is `secure-orch-console/` territory.
9. **Cross-product SSO out of v0.1.** FQ5 lock. No federation hooks, no
   future-flagging.
10. **Frontend bundling: mixed.** OQ4 lock. `frontend/shared/` ships
    `.ts` sources + a pre-built `dist/` artifact. Per-product Vite
    builds `frontend/portal/` + `frontend/admin/` scaffolds.

## Composition pattern

Per-product gateway boots:

1. Calls `runMigrations(db, customerPortalMigrationsDir)`.
2. Calls `runMigrations(db, productMigrationsDir)`.
3. Wires the shell's `Wallet` impl with its DB handle and pepper.
4. Wires the shell's `AuthResolver` with the same DB handle + pepper.
5. Wires the shell's `RateLimiter` with its Redis client.
6. Wires the shell's `StripeClient` with its Stripe creds.
7. Implements its own `RateCardResolver` and passes it to the shell's
   middleware composer.
8. Composes Fastify pre-handlers: `authPreHandler` →
   `rateLimitPreHandler` → `walletReservePreHandler` → product-specific
   route handler.
9. Calls the shell's route handler-side `commitOrRefund(req, usage)`
   helper from its product handlers.
