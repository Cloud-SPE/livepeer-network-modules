# `@livepeer-rewrite/customer-portal`

Shared SaaS-shell library for per-product Livepeer rewrite gateways.
Provides API-key auth, customer ledger, Stripe top-ups, operator admin
engine, Fastify pre-handlers, and a Lit + RxJS widget catalog. Each
per-product gateway (`openai-gateway/`, `vtuber-gateway/`,
`video-gateway/`) embeds this package and configures its own Postgres,
Redis, Stripe credentials, and API-key pepper.

This is a **library**, not a deployed service. See
[`AGENTS.md`](./AGENTS.md) and the plan brief at
[`../docs/exec-plans/active/0013-shell-customer-portal-extraction.md`](../docs/exec-plans/active/0013-shell-customer-portal-extraction.md).

## Subpath exports

| Subpath | Purpose |
|---|---|
| `@livepeer-rewrite/customer-portal/auth` | API-key generate/hash/verify, AuthResolver |
| `@livepeer-rewrite/customer-portal/billing` | Wallet, reservations, top-ups |
| `@livepeer-rewrite/customer-portal/payment` | Stripe checkout + webhook |
| `@livepeer-rewrite/customer-portal/middleware` | Fastify pre-handlers |
| `@livepeer-rewrite/customer-portal/admin` | Operator admin engine |
| `@livepeer-rewrite/customer-portal/db` | drizzle pgSchema, migrations runner |
| `@livepeer-rewrite/customer-portal/registry` | Service-registry hooks (placeholder) |

## Build

```
pnpm -F @livepeer-rewrite/customer-portal build
pnpm -F @livepeer-rewrite/customer-portal test
```

## Frontend sub-workspace

`frontend/` is its own pnpm workspace with shared widgets + portal/admin
SPA scaffolds. See [`frontend/README.md`](./frontend/README.md).
