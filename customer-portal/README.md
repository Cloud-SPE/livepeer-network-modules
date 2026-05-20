# `@livepeer-network-modules/customer-portal`

> **Status (2026-05-19):** The per-product gateways that originally
> consumed this library (`openai-gateway/`, `vtuber-gateway/`,
> `video-gateway/`, `daydream-gateway/`) have been removed from this
> repo. `customer-portal` itself is preserved as a shared TS library
> for any future SaaS consumer; references to specific gateway packages
> below are historical context.

Shared SaaS-shell library for per-product Livepeer rewrite gateways.
Provides API-key auth, customer ledger, Stripe top-ups, operator admin
engine, Fastify pre-handlers, and a Lit + RxJS widget catalog. Each
per-product gateway (historically `openai-gateway/`, `vtuber-gateway/`,
`video-gateway/`) embedded this package and configured its own Postgres,
Redis, Stripe credentials, and API-key pepper.

This is a **library**, not a deployed service. The Docker image is a
slim artifact/import-check image for packaging hygiene, not a long-running
runtime. See
[`AGENTS.md`](./AGENTS.md) and the plan brief at
[`../docs/exec-plans/completed/0013-shell-customer-portal-extraction.md`](../docs/exec-plans/completed/0013-shell-customer-portal-extraction.md).

## Subpath exports

| Subpath | Purpose |
|---|---|
| `@livepeer-network-modules/customer-portal/auth` | API-key generate/hash/verify, AuthResolver |
| `@livepeer-network-modules/customer-portal/billing` | Wallet, reservations, top-ups |
| `@livepeer-network-modules/customer-portal/payment` | Stripe checkout + webhook |
| `@livepeer-network-modules/customer-portal/middleware` | Fastify pre-handlers |
| `@livepeer-network-modules/customer-portal/admin` | Operator admin engine |
| `@livepeer-network-modules/customer-portal/db` | drizzle pgSchema, migrations runner |
| `@livepeer-network-modules/customer-portal/registry` | Service-registry hooks (placeholder) |

## Build

```
pnpm -F @livepeer-network-modules/customer-portal build
pnpm -F @livepeer-network-modules/customer-portal test
```

## Frontend sub-workspace

`frontend/` is its own pnpm workspace with shared widgets + portal/admin
SPA scaffolds. See [`frontend/README.md`](./frontend/README.md).
