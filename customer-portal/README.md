# `@livepeer-network-modules/customer-portal`

Shared SaaS-shell library for Livepeer rewrite consumers. Provides
API-key auth, customer ledger, Stripe top-ups, operator admin engine,
Fastify pre-handlers, and a Lit + RxJS widget catalog. Historical
consumers can embed the same package and supply their own Postgres,
Redis, Stripe credentials, and API-key pepper.

This is a **library**, not a deployed service. The Docker image is a
slim artifact/import-check image for packaging hygiene, not a long-running
runtime. See
[`AGENTS.md`](./AGENTS.md).

## Subpath exports

| Subpath | Purpose |
|---|---|
| `@livepeer-network-modules/customer-portal/auth` | API-key generate/hash/verify, AuthResolver |
| `@livepeer-network-modules/customer-portal/billing` | Wallet, reservations, top-ups |
| `@livepeer-network-modules/customer-portal/payment` | Stripe checkout + webhook |
| `@livepeer-network-modules/customer-portal/middleware` | Fastify pre-handlers |
| `@livepeer-network-modules/customer-portal/admin` | Operator admin engine |
| `@livepeer-network-modules/customer-portal/db` | drizzle pgSchema, migration utilities |
| `@livepeer-network-modules/customer-portal/registry` | Service-registry hooks (placeholder) |
| `@livepeer-network-modules/customer-portal/routes` | Customer self-service Fastify routes |

## Build

```
pnpm -F @livepeer-network-modules/customer-portal build
pnpm -F @livepeer-network-modules/customer-portal test
```

## Frontend sub-workspace

`frontend/` is its own pnpm workspace with shared widgets + portal/admin
SPA scaffolds. See [`frontend/README.md`](./frontend/README.md).
