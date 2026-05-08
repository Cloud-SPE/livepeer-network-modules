# AGENTS.md

This is `openai-gateway/` — the OpenAI-compatible gateway for the Livepeer
network rewrite. Real OpenAI client SDKs hit this gateway → customer-portal
SaaS shell → wire-spec headers → capability-broker → workload runners. The
collapse of the suite's two-package shape (`livepeer-openai-gateway-core` +
`livepeer-openai-gateway`) into a single rewrite component is plan
[0013-openai](../docs/exec-plans/completed/0013-openai-gateway-collapse.md).

Component-local agent map. Repo-root [`../AGENTS.md`](../AGENTS.md) is the
cross-cutting map.

## Operating principles

Inherited from the repo root. Plus:

- **Single-package collapse.** No `-core` engine + shell split. The
  customer-portal/ shared library carries auth + ledger + Stripe + admin
  engine + middleware; this component layers OpenAI-specific routes,
  rate-card schema, and per-OpenAI-endpoint dispatchers on top.
- **Consume customer-portal, do not duplicate it.** Workspace dep
  `@livepeer-rewrite/customer-portal` is the only source of customer
  identity, ledger movement, Stripe webhook handling, admin SPA, and
  shared middleware. New SaaS surfaces land in customer-portal/, not here.
- **Wire surface stays per-product.** Per-OpenAI-endpoint dispatch + route
  → mode mapping is product-specific and lives in this component's
  `src/routes/` + `src/livepeer/`. HTTP-family modes (`http-reqresp`,
  `http-stream`, `http-multipart`) use the inlined `src/livepeer/` shim;
  non-HTTP modes import from `@tztcloud/livepeer-gateway-middleware`
  (workspace dep on `gateway-adapters/ts/`). The `/v1/realtime` route
  consumes `modes.wsRealtime.connect` from that package.
- **Rate-card schema is product-owned.** `migrations/0001_openai_rate_cards.sql`
  ships rate_card_chat_tiers / chat_models / embeddings / audio / images
  tables in this component's namespace; the customer-portal `app.*`
  schema does not reference them.
- **Mainnet only.** No testnets. Smoke deploys against Arbitrum One per
  the cross-component migration brief.

## Where to look

| Question | File |
|---|---|
| What is this component? | [`README.md`](./README.md) |
| Architectural overview | [`DESIGN.md`](./DESIGN.md) |
| Build / run / smoke gestures | [`Makefile`](./Makefile) |
| Compose stack | [`compose.yaml`](./compose.yaml) |
| The shell library this consumes | [`../customer-portal/`](../customer-portal/) |
| The broker it talks to | [`../capability-broker/`](../capability-broker/) |
| The wire spec | [`../livepeer-network-protocol/`](../livepeer-network-protocol/) |
| The collapse plan | [`../docs/exec-plans/completed/0013-openai-gateway-collapse.md`](../docs/exec-plans/completed/0013-openai-gateway-collapse.md) |

## Integration pattern with customer-portal

The customer-portal/ workspace package is consumed via `package.json`
`"@livepeer-rewrite/customer-portal": "workspace:*"`. Imports resolve
through the namespaced subpath exports:

```ts
import { createCustomerPortal } from '@livepeer-rewrite/customer-portal';
import { auth, billing, middleware, admin, db, repo } from '@livepeer-rewrite/customer-portal';
```

The collapse boundaries:

- **customer-portal owns**: customer identity, API-key auth, prepaid-quota
  wallet, Stripe checkout + webhook, admin engine (customers / topups /
  audit), idempotency middleware, rate-limit middleware, shared portal +
  admin SPA shells, repo helpers.
- **openai-gateway owns**: per-OpenAI-endpoint routes (chat / embeddings /
  audio-transcriptions / audio-speech / images-generations / realtime),
  rate-card schema + materialization (chat tiers + model glob, embeddings,
  audio, images), product-specific admin SPA extras (rate-card pages),
  Livepeer wire headers + payment minting + route selection + mode
  dispatch for HTTP-family modes (the `ws-realtime` mode driver is consumed from the
  `@tztcloud/livepeer-gateway-middleware` workspace dep), broker forward.
  The `/v1/realtime` endpoint is a WebSocket upgrade that mints a payment
  and bridges customer frames to the broker via `modes.wsRealtime.connect`.
- **Per-product RateCardResolver impl** lives in `src/service/pricing/`;
  the resolver interface is owned by customer-portal/ (or, until it ships
  there, defined locally and lifted later).

## Doing work in this component

- Docker-first per core belief #15. Use `make build`, `make smoke`.
- TypeScript strict; tsc is the lint gate.
- Migrations boot in order: customer-portal/migrations/ first, then
  openai-gateway/migrations/. The shell's `runMigrations(db, dir)`
  helper is called twice with the two paths from `src/index.ts`.
- New OpenAI endpoints are spec changes — open a plan.
- Suite-citation paths in commit messages must match
  `livepeer-network-suite/livepeer-openai-gateway[-core]/...` verbatim
  per the repo-root AGENTS.md attribution convention.
