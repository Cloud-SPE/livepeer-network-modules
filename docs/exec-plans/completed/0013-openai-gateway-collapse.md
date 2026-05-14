---
plan: 0013-openai
title: openai-gateway absorbs SaaS shell — design
status: design-doc
phase: plan-only
opened: 2026-05-07
owner: harness
related:
  - "active plan 0013-shell — customer-portal/ shared SaaS shell library (foundation; this brief consumes it)"
  - "completed plan 0009 — openai-gateway reference impl (protocol-only; this brief absorbs SaaS)"
  - "completed plan 0008 — gateway-adapters TypeScript middleware (protocol middleware)"
  - "active plan 0008-followup — gateway-adapters non-HTTP modes (parallel work)"
  - "completed plan 0014 — wire-compat envelope + sender daemon"
  - "active plan 0016 — chain-integrated payment-daemon (chain gate for go-live)"
  - "superseded plan 0013-openai-pre-collapse — separate-SaaS-repo proposal (this brief replaces it)"
audience: openai-gateway maintainers planning the suite-shell absorption
---

# Plan 0013-openai — openai-gateway absorbs SaaS shell (design)

> **Paper-only design brief.** No code, no `package.json`, no
> migrations ship from this commit. This brief replaces the
> superseded `0013-suite-openai-gateway-migration-brief.md` (now at
> `docs/exec-plans/superseded/0013-openai-pre-collapse.md`). Locks
> are recorded in §14 as `DECIDED:` blocks; user walks each open
> question before implementation.

## 1. Status and scope

Scope: **collapse the suite's two-package OpenAI gateway** —
`livepeer-network-suite/livepeer-openai-gateway-core/` (engine, npm
package `@cloudspe/livepeer-openai-gateway-core@4.0.1`) +
`livepeer-network-suite/livepeer-openai-gateway/` (Cloud-SPE shell)
— **into the rewrite's existing `openai-gateway/` component**, layering
SaaS surfaces (auth, billing, Stripe, portal, admin) on top of the
protocol-only reference impl from completed plan 0009. The SaaS layer
imports from `customer-portal/` (this brief's foundation, plan
0013-shell). The wire layer imports from `gateway-adapters/` (plans
0008 + 0008-followup). No code is duplicated across components.

This brief is **chain-gated**: it emits payments via `payment-daemon/`
sender mode; production cutover gates on plan 0016 (chain-integrated
payment-daemon) reaching v1.0.0. Pre-1.0.0 implementation against the
stub provider is fine for paper + smoke testing; production smoke on
Arbitrum One per `docs/design-docs/migration-from-suite.md` §3.

Out of scope:

- The orchestrator-side `openai-worker-node` Go process — replaced by
  `capability-broker/` + workload binaries per
  `docs/design-docs/migration-from-suite.md` §2 row "openai-worker-node";
  see `0013-runners-byoc-migration.md` for the workload-binary side.
- Spec-level mode definitions — `livepeer-network-protocol/modes/` is
  frozen at the modes the reference uses today (`http-reqresp@v0`,
  `http-stream@v0`, `http-multipart@v0`). The `/v1/audio/speech`
  binary-stream gap is forwarded to a future spec-level plan; see §15.
- The shared shell library extraction itself — that's plan 0013-shell.
- Per-product VTuber and video gateways — those are 0013-vtuber and
  0013-video.

The component target is **single-package** (`openai-gateway/`), Fastify
5 + Zod 3 + drizzle-orm + ESM TS 5 + Node 20+, MIT-licensed, in the
pnpm workspace. The `-core` suffix is retired; the suite shell's
`@cloudspe/livepeer-openai-gateway-core@4.0.1` is `npm deprecate`-marked
or `npm unpublish`-ed at phase 5.

## 2. What predecessor work left unfinished

**Completed plan 0009** shipped `openai-gateway/` as the rewrite's
protocol-only reference impl: ~600 LOC, three routes
(`chat-completions`, `embeddings`, `audio-transcriptions`), one
sender-daemon call, one broker forward (`POST /v1/cap` with
Livepeer-Mode dispatch), no auth, no billing, no UI. Wire-correct;
SaaS-blank.

**Superseded plan 0013-openai-pre-collapse** sketched two paths for
absorbing the suite shell: (A) collapse into one OSS package + a
separate SaaS repo, (B) keep two packages but rename. Both are now
obsolete. The user walk on 2026-05-06 reversed the separate-repo cut
in favour of a single-package collapse with shared SaaS-shell library
extraction (plan 0013-shell).

**Completed plan 0008** ships `gateway-adapters/` HTTP-family
middleware. **Active plan 0008-followup** extends it to non-HTTP
modes; this brief is disjoint from 0008-followup (the openai-gateway
only consumes HTTP modes).

**Completed plan 0014** locked the wire-compat envelope; the
sender-daemon side is in production. **Active plan 0016** ports
`payment-daemon/`'s providers to Arbitrum One. The collapsed
openai-gateway's chain-side correctness rides on 0016.

**The suite's two-package split** is unwound entirely. The `-core`
engine + the suite shell collapse into one workspace component. The
`Wallet` / `AuthResolver` / `RateLimiter` interfaces hide the wire
layer from the SaaS layer; that boundary is preserved across the
collapse but no longer crosses a package boundary.

## 3. Reference architecture

```
customer (curl, OpenAI SDK, agent)
   │  POST /v1/chat/completions
   │  Authorization: Bearer sk-live-…
   │  Idempotency-Key: …
   ▼
openai-gateway/  (this brief)
   │
   │  Fastify pre-handlers (composed left → right):
   │    1. authPreHandler        ← customer-portal/ apiKeyAuthResolver
   │    2. rateLimitPreHandler   ← customer-portal/ rateLimiter
   │    3. idempotencyPreHandler ← customer-portal/ middleware
   │    4. metricsHook           ← customer-portal/ (or local)
   │
   │  per-route handler (chat-completions, embeddings, audio-*, images-*, speech):
   │    5. wallet.reserve(callerId, costQuote)         ← customer-portal/ Wallet
   │    6. payerDaemon.createPayment(face_value, …)    ← payment-daemon/ gRPC sender
   │    7. gatewayAdapters.httpReqResp.send(…)         ← gateway-adapters/ http-reqresp@v0
   │       (or http-stream / http-multipart per route)
   │       Headers: Livepeer-Capability, Livepeer-Offering, Livepeer-Payment,
   │                Livepeer-Spec-Version, Livepeer-Mode, Livepeer-Request-Id
   │    8. parse usage from response                   ← per-route extractor
   │    9. wallet.commit(handle, usage)                ← customer-portal/ Wallet
   │
   ▼  forwards to
capability-broker  (POST /v1/cap, dispatch by Livepeer-Mode)
   │
   ▼  validates payment, mints/redeems, dispatches to
workload runner (vLLM, Ollama, Whisper, kokoro-tts, diffusers)
```

Side-channels:

```
admin SPA (operator)              portal SPA (customer)
  /v1/admin/*  ← basic-auth         /v1/account/*  ← cookie session
                                    /v1/billing/topup/checkout
                                    Stripe Checkout redirect ↓
                                    /v1/stripe/webhook  ← Stripe → idempotent on event.id
```

Wire spec: `livepeer-network-protocol/headers/livepeer-headers.md`,
modes at `livepeer-network-protocol/modes/{http-reqresp,http-stream,http-multipart}@v0.md`.
Reference emission sites in the rewrite:
`openai-gateway/src/livepeer/headers.ts:5-15`,
`openai-gateway/src/livepeer/payment.ts:64`,
`openai-gateway/src/livepeer/http-reqresp.ts:27-32`,
`openai-gateway/src/livepeer/http-stream.ts:30-38`,
`openai-gateway/src/livepeer/http-multipart.ts:29-34`.

## 4. Component layout

`openai-gateway/` after collapse:

```
openai-gateway/
  AGENTS.md                          ← entry-point map
  DESIGN.md                          ← updated; section on SaaS surfaces added
  README.md                          ← consumer/operator front door
  Makefile                           ← `make build|test|lint|smoke|migrate`
  Dockerfile                         ← multi-stage; SaaS portal/admin bundles + Node runtime
  compose.yaml                       ← extends rewrite reference; adds Postgres + Redis services
  compose.prod.yaml                  ← prod overlay (no port exposes; named volumes)
  package.json                       ← @livepeer-network-modules/openai-gateway; ESM-only
  pnpm-workspace not needed (root manages)
  tsconfig.json                      ← extends repo root tsconfig.base.json
  vitest.config.ts
  drizzle.config.ts                  ← points at ./src/db/schema.ts; output ./migrations
  migrations/                        ← product-specific (pricing tables; usage rollups)
    0000_openai_init.sql             ← rate_card_chat_tiers / models / embeddings / images / speech / transcriptions
    0001_retail_pricing.sql          ← retail_price_catalog / retail_price_aliases
    0002_usage_records.sql           ← per-request usage rollups
  scripts/
    smoke.sh                         ← end-to-end smoke against compose stack
    issue-key.ts                     ← admin CLI to mint a customer + key
  src/
    main.ts                          ← composition root (extends current index.ts)
    config.ts                        ← Zod env schemas; merges shell + product config
    server.ts                        ← Fastify factory
    livepeer/                        ← (existing) wire surface
      headers.ts                     ← unchanged
      payment.ts                     ← unchanged
      http-reqresp.ts                ← unchanged
      http-stream.ts                 ← unchanged
      http-multipart.ts              ← unchanged
      errors.ts                      ← unchanged
    routes/                          ← (existing) protocol routes; NEW + extended
      chat-completions.ts            ← extends current; adds streaming pass-through (§4.5)
      embeddings.ts                  ← extends current
      audio-transcriptions.ts        ← extends current; multipart path
      audio-speech.ts                ← NEW; 503 + Livepeer-Error: mode_unsupported (§ 5.4 of superseded brief; spec gap)
      images-generations.ts          ← NEW; first adopter for /v1/images/generations
      images-edits.ts                ← NEW; first adopter for /v1/images/edits
      account.ts                     ← NEW shell-routed (delegates to customer-portal/)
      admin.ts                       ← NEW shell-routed (delegates to customer-portal/)
      billing-topup.ts               ← NEW shell-routed
      stripe-webhook.ts              ← NEW shell-routed
    pricing/
      rateCard.ts                    ← product-specific RateCardResolver impl (Postgres-backed)
      glob.ts                        ← model-or-pattern matcher (ported from suite)
      pricing.test.ts                ← table-driven pricing tests
    repo/
      usageRecords.ts                ← drizzle CRUD over the product `usage_records` table
      rateCardChat.ts                ← rate-card lookup repo
      rateCardEmbeddings.ts
      rateCardImages.ts
      rateCardSpeech.ts
      rateCardTranscriptions.ts
      retailPriceCatalog.ts
      retailPriceAliases.ts
    runtime/
      tokenizer/
        tiktoken.ts                  ← (ported from suite providers/tokenizer/tiktoken.ts)
      streaming/
        sseSettlement.ts             ← three-path settlement (commit-on-usage, refund-on-no-token, estimate-on-no-usage)
    frontend/                        ← per-product admin extras
      admin/
        components/
          admin-rate-card-chat.ts        ← (ported from suite admin SPA)
          admin-rate-card-embeddings.ts
          admin-rate-card-images.ts
          admin-rate-card-speech.ts
          admin-rate-card-transcriptions.ts
          admin-retail-pricing-capability.ts
        index.html                       ← admin shell entry (imports customer-portal admin SPA + product extras)
      portal/
        index.html                       ← portal shell entry (imports customer-portal portal SPA verbatim)
  test/
    integration/                     ← Postgres + Redis-backed, no live workers
    smoke/                           ← compose-stack smoke
    mock-runner/                     ← FastAPI shim Docker image; canned vLLM-shaped chat / embeddings / transcriptions / speech / images responses for offline `make smoke`
```

`gateway-adapters/` and `customer-portal/` enter via `package.json`
`dependencies` (workspace `*`).

## 5. Source-to-destination file map

### 5.1 Engine → openai-gateway/src/

| Source | Target | Notes |
|---|---|---|
| `livepeer-network-suite/livepeer-openai-gateway-core/src/runtime/http/chat/completions.ts:44-187` | `openai-gateway/src/routes/chat-completions.ts` | Existing reference impl already covers the non-streaming path; this port lifts the multi-mode dispatch + streaming-vs-non-streaming branching. |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/runtime/http/chat/streaming.ts` | `openai-gateway/src/routes/chat-completions.ts` (streaming branch) | True pass-through (no buffering) per §4.5 of superseded brief. Use `http-stream.send` plumbing instead of `await arrayBuffer`. |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/runtime/http/embeddings/index.ts:36` | `openai-gateway/src/routes/embeddings.ts` | Existing reference impl extended with shell pre-handlers. |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/runtime/http/audio/transcriptions.ts:40` | `openai-gateway/src/routes/audio-transcriptions.ts` | Multipart; existing reference impl extended. Suite's `dispatch/transcriptions.ts:122-142` (multipart hand-roll) + `:207-237` (boundary builder) port verbatim. Worker's `x-livepeer-audio-duration-seconds` response header retired; duration → `Livepeer-Work-Units`. |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/runtime/http/audio/speech.ts:38` | `openai-gateway/src/routes/audio-speech.ts` | 503 + `Livepeer-Error: mode_unsupported` until `http-binary-stream@v0` ships (separate spec plan; see §15). |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/runtime/http/images/generations.ts:40` | `openai-gateway/src/routes/images-generations.ts` | Reference doesn't ship images today; first-adopter port. JSON body, fits `http-reqresp@v0`. |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/dispatch/chatCompletion.ts:62-187` | folded into `openai-gateway/src/routes/chat-completions.ts` | Per-request lifecycle (reserve → payer-daemon → broker forward → commit). Suite's `selectNode` + `quoteCache` are dropped; broker URL replaces them. |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/dispatch/streamingChatCompletion.ts:56-83` | `openai-gateway/src/runtime/streaming/sseSettlement.ts` | Three-path settlement logic (commit-on-usage / refund-on-no-token / estimate-on-no-usage). Wire-independent; survives migration. |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/providers/nodeClient.ts:99-124` | **deleted** | Capability + quote schemas obsolete; broker dispatches by `Livepeer-Mode`. |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/providers/nodeClient/fetch.ts:79-241` | **deleted** | Six per-OpenAI-endpoint POSTs replaced by one `gateway-adapters/` send per mode. |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/providers/payerDaemon.ts:50` | replaced by direct `payment-daemon/` gRPC call (proto in `livepeer-network-protocol/proto/livepeer/payments/v1/payer_daemon.proto:43`) | Suite gRPC stubs (`src/providers/payerDaemon/gen/...`) re-genned against rewrite proto by the implementing agent. |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/providers/serviceRegistry.ts:11` | **deleted** | Quote-free flow; no resolver/registry on the gateway. Daemon resolves recipients itself. |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/service/routing/quoteRefresher.ts` | **deleted** | Quote-free flow. |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/service/payments/createPayment.ts:40` | folded into `openai-gateway/src/routes/*.ts` | Body collapses: `payerDaemon.createPayment({ face_value, recipient, capability, offering })` per spec. |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/service/payments/sessions.ts` | **deleted** | `StartSession`/`SessionCache` are receiver-side; sender no longer carries them. |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/service/routing/circuitBreaker.ts` | folded into `openai-gateway/src/routes/*.ts` only if used | Keep if it earns its weight per-broker-URL; otherwise drop. |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/service/pricing/index.ts` + `glob.ts` + `rateCardLookup.ts` | `openai-gateway/src/pricing/` | Rate-card resolver impl (DB-backed); ports via `customer-portal/`'s `RateCardResolver` interface. |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/service/tokenAudit/index.ts` | `openai-gateway/src/runtime/tokenAudit.ts` | Token usage audit subsystem (commit-time validation). |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/providers/tokenizer/tiktoken.ts` | `openai-gateway/src/runtime/tokenizer/tiktoken.ts` | Embedded tiktoken encoder for streaming-estimation fallback. |
| `livepeer-network-suite/livepeer-openai-gateway-core/src/types/capability.ts:14-21` | `openai-gateway/src/livepeer/capabilityMap.ts` | Capability string canonical map; format `<domain>:<uri-path>` already matches spec. |

### 5.2 Shell → openai-gateway/src/

| Source | Target | Notes |
|---|---|---|
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/main.ts:32-64` | `openai-gateway/src/main.ts` | Composition root pattern; rewrite version imports from `customer-portal/` instead of `@cloudspe/livepeer-openai-gateway-core`. |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/runtime/http/account/routes.ts` | `openai-gateway/src/routes/account.ts` | Thin delegator; calls `customer-portal/`'s `registerAccountRoutes`. |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/runtime/http/admin/routes.ts` | `openai-gateway/src/routes/admin.ts` | Delegator + product-specific admin extras (rate-card admin endpoints stay here). |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/runtime/http/admin/pricing.ts` | `openai-gateway/src/routes/admin.ts` (folded) | Rate-card admin endpoints. |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/runtime/http/billing/topup.ts` | `openai-gateway/src/routes/billing-topup.ts` | Delegator; calls `customer-portal/`'s Checkout factory. |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/runtime/http/portal/static.ts` | `openai-gateway/src/runtime/http/portal-static.ts` | Static-file serving for portal SPA bundle. |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/runtime/http/admin/console/static.ts` | `openai-gateway/src/runtime/http/admin-static.ts` | Static-file serving for admin SPA bundle. |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/runtime/http/stripe/webhook.ts` | `openai-gateway/src/routes/stripe-webhook.ts` | Delegator; ships customer-portal's webhook handler (no openai-specific events). |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/runtime/http/middleware/idempotency.ts` | `customer-portal/src/middleware/idempotency.ts` | Already covered by 0013-shell §5.4. |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/service/auth/keys.ts` | `customer-portal/src/auth/apiKey.ts` | Already covered by 0013-shell §5.1. |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/service/billing/wallet.ts` | `customer-portal/src/billing/wallet.ts` | Already covered by 0013-shell §5.2. |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/service/pricing/rateCard.ts` | `openai-gateway/src/pricing/rateCard.ts` | Product-specific (rate-card semantics differ across products). |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/repo/{customers,apiKeys,reservations,topups,stripeWebhookEvents,adminAuditEvents,idempotency}.ts` | `customer-portal/src/repo/*.ts` | Already covered by 0013-shell §5.2 + 5.3 + 5.4. |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/migrations/0001_rate_card.sql` + `0002_seed_rate_card.sql` | `openai-gateway/migrations/0000_openai_init.sql` | Product-specific rate-card tables; renumbered from openai-gateway 0000. |
| `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/migrations/0004_retail_pricing.sql` | `openai-gateway/migrations/0001_retail_pricing.sql` | Retail-pricing tables. |
| `livepeer-network-suite/livepeer-openai-gateway/frontend/admin/components/admin-rate-card-{chat,embeddings,images,speech,transcriptions}.js` | `openai-gateway/src/frontend/admin/components/admin-rate-card-*.ts` | TS-ported per 0013-shell Q5; product-specific, lives here. |
| `livepeer-network-suite/livepeer-openai-gateway/frontend/admin/components/admin-retail-pricing-capability.js` | `openai-gateway/src/frontend/admin/components/admin-retail-pricing-capability.ts` | Product-specific. |
| `livepeer-network-suite/livepeer-openai-gateway/frontend/portal/index.html` | `openai-gateway/src/frontend/portal/index.html` | Portal shell entry; loads `customer-portal/` portal SPA verbatim. |
| `livepeer-network-suite/livepeer-openai-gateway/frontend/admin/index.html` | `openai-gateway/src/frontend/admin/index.html` | Admin shell entry; loads `customer-portal/` admin SPA + product extras. |

### 5.3 Build / dev-loop

| Source | Target |
|---|---|
| `livepeer-network-suite/livepeer-openai-gateway/Dockerfile` | `openai-gateway/Dockerfile` |
| `livepeer-network-suite/livepeer-openai-gateway/compose.yaml` | merged with `livepeer-network-rewrite/openai-gateway/compose.yaml` (existing reference compose extended with Postgres + Redis services from suite compose) |
| `livepeer-network-suite/livepeer-openai-gateway/compose.prod.yaml` | `openai-gateway/compose.prod.yaml` |

## 6. Tech-stack lock + variance justification

**Canonical stack — no variance.** Fastify 5 + Zod 3 + drizzle-orm +
ESM TypeScript 5 + Node 20+ + pnpm workspace + Postgres 16 + Redis 7
+ Stripe SDK 14 + Lit 3 + RxJS 7. Same as `customer-portal/` (plan
0013-shell §6).

The suite is on Fastify 4; the migration bumps to Fastify 5. The
delta is small (route-options typing; some plugin lifecycle
adjustments). Done as part of the port, not a separate plan.

The suite uses npm workspaces; the rewrite uses pnpm. Lockfile is
fresh; no `package-lock.json` retained.

`test/mock-runner/` (per §14 OQ1 lock) is the lone Python surface in
`openai-gateway/`'s deployment artefacts: a separate Docker image
(sibling to the gateway image, tag tracks the gateway's tag) shipping
a minimal FastAPI shim. Mirrors the conformance runner's
fixtures+ffmpeg pattern. The gateway runtime itself stays Node-only.

## 7. DB schema

The openai-gateway owns one **product schema** in addition to consuming
`customer-portal/`'s `app.*` schema:

| Table | Purpose | Source |
|---|---|---|
| `app.rate_card_chat_tiers` | Tier → input/output USD per million tokens. | suite shell `repo/schema.ts:164-169` |
| `app.rate_card_chat_models` | Model-or-pattern → tier. | suite shell `repo/schema.ts:171-187` |
| `app.rate_card_embeddings` | Per-model USD per million tokens. | suite shell `repo/schema.ts:189-206` |
| `app.rate_card_images` | Per-model + size + quality USD per image. | suite shell `repo/schema.ts:208-224` |
| `app.rate_card_speech` | Per-model USD per million chars. | suite shell `repo/schema.ts:226-243` |
| `app.rate_card_transcriptions` | Per-model USD per minute. | suite shell `repo/schema.ts:245-259` |
| `app.retail_price_catalog` | (capability, offering, customer_tier) → USD per unit. | suite shell `repo/schema.ts:268-289` |
| `app.retail_price_aliases` | (capability, model-or-pattern) → offering. | suite shell `repo/schema.ts:291-314` |
| `openai.usage_records` | Per-request usage rollups (callerId, capability, model, tokens, cents). | NEW — suite has it implicit in shell-engine seam; rewrite makes it explicit in the openai schema. |
| `openai.usage_rollups_daily` | Daily-aggregated usage (FK customer_id) for portal dashboard. | suite shell `repo/usageRollups.ts` (test file at `usageRollups.test.ts`) |

**Schema namespace**: per plan 0013-shell §7, the rate-card tables
move from `app.*` to a per-product schema. **DECIDED Q1 below**:
keep them in `app.*` as a pragmatic exception, because the admin
SPA's rate-card pages are the only consumer and they live in the
openai-gateway today. Re-namespacing requires an admin SPA refactor
without operator benefit. The shell schema doesn't FK into rate-card,
so there's no boundary leak.

The `openai.usage_records` + `openai.usage_rollups_daily` tables are
in a fresh `openai` namespace to avoid colliding with future product
gateways' rollup tables.

Migration filenames:
- `openai-gateway/migrations/0000_openai_init.sql` — rate-card tables.
- `openai-gateway/migrations/0001_retail_pricing.sql` — retail-pricing tables.
- `openai-gateway/migrations/0002_usage_records.sql` — `openai.usage_records` + `openai.usage_rollups_daily`.

Boot order: `customer-portal/migrations/` first, then
`openai-gateway/migrations/`. The shell's `runMigrations(db, dir)`
helper takes the directory; `openai-gateway/src/main.ts` calls it
twice with the two paths.

## 8. Customer-facing surfaces

### 8.1 UI flows

| Flow | Component | Lives in |
|---|---|---|
| Customer login / dashboard / API keys / top-up / settings | portal-* | `customer-portal/` (shared) |
| Operator login / health / customers / topups / audit / nodes / reservations | admin-* | `customer-portal/` (shared) |
| Operator rate-card edit (chat / embeddings / images / speech / transcriptions) | admin-rate-card-* | `openai-gateway/src/frontend/admin/components/` (product-specific) |
| Operator retail-pricing per-capability | admin-retail-pricing-capability | `openai-gateway/src/frontend/admin/components/` (product-specific) |

Portal SPA is the shell verbatim; admin SPA is the shell + 6 extra
Lit components. Bundle output is per-component (Vite); compose
serves under `/portal/...` and `/admin/...` static paths.

### 8.2 API endpoints

**Shell-owned** (delegate to `customer-portal/`): see plan 0013-shell §8.2.

**Product-owned** (this brief):

| Method + path | Mode | Capability | Notes |
|---|---|---|---|
| `POST /v1/chat/completions` | `http-reqresp@v0` (non-stream) / `http-stream@v0` (stream:true) | `openai:/v1/chat/completions` | Suite's existing route; the streaming branch uses true pass-through (no buffering). |
| `POST /v1/embeddings` | `http-reqresp@v0` | `openai:/v1/embeddings` | Suite's existing route. |
| `POST /v1/audio/transcriptions` | `http-multipart@v0` | `openai:/v1/audio/transcriptions` | Multipart body; duration → `Livepeer-Work-Units`. |
| `POST /v1/audio/speech` | (none — 503 + `Livepeer-Error: mode_unsupported`) | `openai:/v1/audio/speech` | Spec gap (`http-binary-stream@v0` not yet defined). See §15. |
| `POST /v1/images/generations` | `http-reqresp@v0` | `openai:/v1/images/generations` | First adopter; JSON URLs or base64-inline both fit. |
| `POST /v1/images/edits` | `http-multipart@v0` | `openai:/v1/images/edits` | First adopter; multipart with image + mask + prompt. |
| `GET /v1/admin/pricing/chat` | (admin) | n/a | Rate-card chat get/set. |
| `GET /v1/admin/pricing/embeddings` | (admin) | n/a | |
| `GET /v1/admin/pricing/images` | (admin) | n/a | |
| `GET /v1/admin/pricing/speech` | (admin) | n/a | |
| `GET /v1/admin/pricing/transcriptions` | (admin) | n/a | |

### 8.3 Header set per outbound call

Per `livepeer-network-protocol/headers/livepeer-headers.md`:

- `Livepeer-Capability` — e.g. `openai:/v1/chat/completions`.
- `Livepeer-Offering` — typically the model id (e.g.
  `vllm-h100-batch4` or just `gpt-4o`); operator-named.
- `Livepeer-Payment` — base64(`livepeer.payments.v1.Payment`) bytes.
- `Livepeer-Spec-Version` — `0.1`.
- `Livepeer-Mode` — `http-reqresp@v0` / `http-stream@v0` /
  `http-multipart@v0`.
- `Livepeer-Request-Id` — uuid; always emitted (per §14 OQ4 lock). The
  gateway synthesises a fresh `crypto.randomUUID()` (or callerId-derived
  uuid) per request when the customer doesn't supply one; if the
  customer supplies a `Livepeer-Request-Id` header, the gateway respects
  the customer-supplied value verbatim. Helps operator debugging across
  the broker hop.

The suite's lowercase `livepeer-payment` is renamed to canonical case;
the other five headers are NEW. Six call sites in `nodeClient/fetch.ts`
collapse to one `gateway-adapters/` send per route.

### 8.4 OAuth, chat workers, egress workers

OAuth: not in v0.1 (per plan 0013-shell §8.3). Chat workers: not a
shell concern — workload runners are the openai-runners brief
(`0013-runners-byoc-migration.md`). Egress workers: not applicable to
OpenAI gateway (no customer-direction streaming egress beyond SSE
pass-through, which is part of the route handler).

## 9. Cross-component dependencies

```
openai-gateway/
  package.json:
    dependencies:
      "@livepeer-network-modules/customer-portal":  "workspace:*"
      "@livepeer-network-modules/gateway-adapters": "workspace:*"
      "@livepeer-network-modules/livepeer-network-protocol-go": (no — Go-only; openai-gateway is TS)
      fastify, zod, drizzle-orm, pg, ioredis, stripe, lit, rxjs
```

Imports:

- From `customer-portal/`:
  - `auth` — `createApiKeyAuthResolver`, `createAdminBasicAuth`,
    `authPreHandler`, `adminBasicAuthPreHandler`.
  - `billing` — `createPrepaidQuotaWallet`.
  - `middleware` — `createRateLimiter`, `rateLimitPreHandler`,
    `idempotencyPreHandler`, `idempotencyOnSend`, `toHttpError`.
  - `db` — `createPgDatabase`, `runMigrations`.
  - `repo` — `customers`, `apiKeys`, `reservations`, `topups`,
    `stripeWebhookEvents`, `adminAuditEvents`, `idempotencyRequests`.
  - `service` — `createAuthService`, `createEngineAdminService`.
  - `stripe` — `registerStripeWebhookRoute`, `createSdkStripeClient`.
  - `frontend/portal` + `frontend/admin` + `frontend/shared` —
    bundled SPAs.

- From `gateway-adapters/`:
  - `modes` — `httpReqRespSend`, `httpStreamSend`, `httpMultipartSend`.
  - `payment` — `createPayerDaemonClient` (gRPC unix-socket sender).
  - `headers` — `LivepeerHeader.*` constant strings.

- From `livepeer-network-protocol/`:
  - `proto-go` (no — TS uses generated `proto/` stubs via
    `gateway-adapters/`).

The openai-gateway component is **a leaf** — no other component
imports `openai-gateway/`. It depends on three foundations
(`customer-portal/`, `gateway-adapters/`, the wire spec).

## 10. Configuration surface

### 10.1 Env vars (product-specific; in addition to `customer-portal/` env)

| Env var | Required | Purpose |
|---|---|---|
| `LIVEPEER_BROKER_URL` | yes | The orch's `capability-broker` HTTP base URL. Single value per orch identity. |
| `LIVEPEER_PAYER_DAEMON_SOCKET` | yes | Unix-socket path to local sender-mode `payment-daemon`. Default `/var/run/livepeer/payment.sock` (matches reference `openai-gateway/compose.yaml:129`). |
| `LIVEPEER_PAYER_DEFAULT_FACE_VALUE_WEI` | yes | Default `face_value` for `payerDaemon.createPayment`. Reference uses `1000n` (`openai-gateway/src/livepeer/payment.ts:64`); operator tunes per traffic profile. |
| `LIVEPEER_SPEC_VERSION` | no (default `0.1`) | Header value emitted in `Livepeer-Spec-Version`. |
| `BROKER_CALL_TIMEOUT_MS` | no (default `30000`) | Per-broker-URL HTTP timeout. Renamed from suite's `nodeCallTimeoutMs`. |
| `OPENAI_DEFAULT_OFFERING_PER_CAPABILITY` | no | YAML on disk at `/etc/openai-gateway/offerings.yaml` (per §14 OQ2 lock). Operator mounts read-only; capability-id → default offering id mapping. Falls back to per-request rate-card if unset. Schema in `openai-gateway/docs/operator-runbook.md` §"Per-capability default offering". |
| `OPENAI_AUDIO_SPEECH_ENABLED` | no (default `false`) | When false, route returns 503 + `Livepeer-Error: mode_unsupported`. Flips to `true` once `http-binary-stream@v0` ships. |

### 10.2 YAML config (optional)

Per §14 OQ2 lock: operator supplies a YAML file mounted read-only at
`/etc/openai-gateway/offerings.yaml`. Operator-friendly (multi-line,
comments, version-control diffable); env-var JSON was rejected as
harder to maintain when the offering catalog grows. Sample shape
(capability-id → default offering id mapping):

```yaml
# /etc/openai-gateway/offerings.yaml — operator mount (read-only)
defaults:
  "openai:/v1/chat/completions":
    streaming: vllm-h100-stream
    non-streaming: vllm-h100-batch4
  "openai:/v1/embeddings":
    default: bge-large-en
  "openai:/v1/audio/transcriptions":
    default: whisper-large-v3
  "openai:/v1/images/generations":
    default: realvis-xl-v4-lightning
```

This is **per-deployment operator config**, not customer-tunable. The
gateway falls back to the rate-card default if a request omits the
offering and the YAML has no entry. Full schema documented in
`openai-gateway/docs/operator-runbook.md` §"Per-capability default
offering".

### 10.3 Config loader

Single `openai-gateway/src/config.ts` Zod schema; `customer-portal/`
exports its own loader and the openai-gateway composes both.

## 11. Conformance / smoke tests

### 11.1 Wire-protocol conformance

Existing rewrite conformance fixtures cover the modes
(`livepeer-network-protocol/conformance/fixtures/http-reqresp/`,
`http-stream/`, `http-multipart/`). The openai-gateway as a *consumer*
of `gateway-adapters/` is verified against those fixtures by
integration tests, not new mode-spec fixtures.

### 11.2 Per-component smokes

`openai-gateway/test/smoke/` (new):

- `chat-completion.smoke.ts` — POST `/v1/chat/completions`
  (non-streaming) against compose stack. Asserts: 200, body contains
  `usage.prompt_tokens` + `usage.completion_tokens`; ledger row in
  `openai.usage_records`; `customers.balance_usd_cents` decremented.
- `chat-completion-stream.smoke.ts` — same with `stream: true`.
  Asserts: SSE pass-through preserves first-token latency under 500ms;
  three-path settlement correctness across (usage-arrives,
  no-first-token, first-token-no-usage).
- `embeddings.smoke.ts` — POST `/v1/embeddings`. Asserts: 200, ledger
  decrements, model lookup by glob hits the expected rate-card row.
- `transcriptions.smoke.ts` — POST `/v1/audio/transcriptions`
  multipart. Asserts: duration extracted from response trailer →
  `Livepeer-Work-Units`; ledger debits per-minute.
- `speech-503.smoke.ts` — POST `/v1/audio/speech`. Asserts: 503 +
  `Livepeer-Error: mode_unsupported`. Toggleable via
  `OPENAI_AUDIO_SPEECH_ENABLED=true` (then expects 200).
- `images-generations.smoke.ts` — POST `/v1/images/generations`.
  Asserts: 200, response contains `data[].url`; ledger debits per
  image-size-quality rate-card row.
- `stripe-topup.smoke.ts` — `POST /v1/billing/topup/checkout` returns
  Checkout URL; replay Stripe `checkout.session.completed` event;
  `app.topups.status = succeeded`; `customers.balance_usd_cents`
  incremented.
- `idempotency.smoke.ts` — POST `/v1/chat/completions` twice with
  same `Idempotency-Key`; second call returns cached body without
  re-charging.

### 11.3 Compose stack

`openai-gateway/compose.yaml`:
- `postgres:16-alpine` (suite parity)
- `redis:7-alpine`
- `payment-daemon` (sender-mode; `tztcloud/livepeer-payment-daemon:v0.8.10` per plan 0014 / current pin)
- `capability-broker` (`tztcloud/livepeer-capability-broker:v0.8.10`)
- `mock-runner` (a tiny FastAPI shim returning canned vLLM-shaped responses; for smoke without GPU)
- `openai-gateway` (this brief)

Compose-level overlays:
- `compose.prod.yaml` — production overlay (no `ports:` exposes; named
  volumes; healthcheck-gated startup).
- `compose.smoke.yaml` — overlay enabling `mock-runner` + asserting
  smoke env vars.

## 12. Operator runbook deltas

`openai-gateway/docs/operator-runbook.md` (NEW):

1. **Compose deploy** — `cp .env.example .env`, fill PG / REDIS /
   STRIPE / API_KEY_PEPPER / ADMIN_BASIC_AUTH_USER + PASS /
   LIVEPEER_BROKER_URL / LIVEPEER_PAYER_DAEMON_SOCKET, mount keystore
   for the colocated payment-daemon, `docker compose up`.
2. **First customer** — admin SPA login → Customers → New → set
   tier, monthly grant, initial balance via top-up; portal SPA
   self-issues an API key.
3. **Rate-card seeding** — admin SPA → Pricing → Chat (and so on);
   set per-tier USD per million; pattern matchers default to `*` →
   tier `default`.
4. **Per-capability default offering** — optional YAML at
   `/etc/openai-gateway/offerings.yaml`; mounts read-only.
5. **Streaming pass-through verification** — section dedicated to
   first-token-latency smoke; reference SSE-passthrough invariant
   from §11.2.
6. **Stripe webhook signing-secret rotation** — `STRIPE_WEBHOOK_SECRET`
   matches Stripe Dashboard webhook endpoint.
7. **API-key pepper** — same advice as `customer-portal/` §12.1
   (rotation invalidates all keys).
8. **Migration cadence** — boot order: `customer-portal/migrations/`
   first, then `openai-gateway/migrations/`; covered by the shell's
   `runMigrations` helper called twice.
9. **Speech endpoint flag** — `OPENAI_AUDIO_SPEECH_ENABLED=false` by
   default (pre-`http-binary-stream@v0`); document the toggle.
10. **Suite cutover** — for operators currently on
    `livepeer-openai-gateway@4.0.1`: parallel-run both gateways
    against the same hot wallet; flip DNS / reverse-proxy when smoke
    passes; suite shell can be turned off after the suite repo's own
    deprecation per `migration-from-suite.md` §3.

`payment-daemon/docs/operator-runbook.md` is unchanged by this brief
(the gateway is just one of many senders).

## 13. Migration sequence

5 phases. Each independently revertable. Phase 4 is the wire cut.

### Phase 1 — Workspace scaffold + dependency wiring

Create `openai-gateway/package.json` workspace deps on
`customer-portal/` + `gateway-adapters/`. Lift `openai-gateway/src/`
from the existing reference impl unchanged. Add empty dirs for
`pricing/`, `repo/`, `frontend/`, `migrations/`. Compose stack
extended with Postgres + Redis services from suite compose.

**Acceptance:** `pnpm -F @livepeer-network-modules/openai-gateway build`
green; existing reference impl smoke (chat / embeddings /
transcriptions per plan 0009) still passes; Postgres + Redis come up
clean. Diff: ~+200 LOC (compose + scaffolding).

### Phase 2 — Pricing / rate-card port

Port `pricing/`, `repo/rateCard*.ts`, `repo/retailPrice*.ts`. Land
`migrations/0000_openai_init.sql` + `0001_retail_pricing.sql` +
`0002_usage_records.sql`. Wire `RateCardResolver` impl; per-route
handlers consult the resolver for cost-quote. Per §14 OQ3 lock, the
images rate-card metadata (`app.rate_card_images`) lands here for the
catalog, but the OpenAI `/v1/images/*` routes and broker dispatch are
deferred to phase 4 — phase 2 is pricing-only, no new customer
endpoints lit.

**Acceptance:** rate-card tests green; admin pricing pages render
against fresh DB. Diff: ~+1,000 LOC ported from suite shell pricing.

### Phase 3 — SaaS pre-handlers + admin / portal wiring

Wire `customer-portal/` middleware: auth → rate-limit → idempotency →
metrics. Land `routes/account.ts`, `routes/admin.ts`,
`routes/billing-topup.ts`, `routes/stripe-webhook.ts` (delegators).
Frontend: copy portal/admin shell index.html + the 6 product-extra
admin Lit components TS-ported.

**Acceptance:** end-to-end stripe-topup smoke green; portal SPA
issues + revokes API keys; admin SPA edits rate-card; auth on every
paid route; idempotency smoke green.

### Phase 4 — Wire cut: rename headers, drop registry, swap RPC shape

Rename `livepeer-payment` → `Livepeer-Payment` (canonical case). Add
`Livepeer-Capability`, `Livepeer-Offering`, `Livepeer-Spec-Version`,
`Livepeer-Mode`, `Livepeer-Request-Id`. Replace
`providers/nodeClient/fetch.ts` with `gateway-adapters/` send per
mode. Drop `serviceRegistry`, `quoteCache`, `quoteRefresher`,
`selectNode`. Swap `payerDaemon.createPayment({ workId, workUnits })`
to `{ face_value, recipient, capability, offering }` per
`payer_daemon.proto:54-71`. Drop `StartSession` + sender-side
`SessionCache`. Re-gen suite gRPC stubs against rewrite proto.

Per §14 OQ3 lock, `images-generations` (and `images-edits`) ships in
this phase: `routes/images-generations.ts` + `routes/images-edits.ts`
land alongside the wire cut, wired through the same `gateway-adapters/`
send and the rate-card scaffolding seeded in phase 2. Customer-facing
image-generation lights up in one cut.

Wire compat: byte-for-byte `Payment` envelope round-trip test against
≥10 fixtures from rewrite's wire-compat corpus.

**Acceptance:** smoke against `capability-broker` + receiver-mode
`payment-daemon` + mock runner — six endpoints (chat, chat-stream,
embeddings, transcriptions, images-generations, speech-503), full
lifecycle including refunds; new admin SPA reflects the
post-collapse pricing surface; LOC delta -2,200/-2,300 net (cross-
package plumbing collapses).

### Phase 5 — Streaming pass-through + suite shell deprecate

Verify streaming dispatcher preserves true pass-through (Node
`http.request` unbuffered + broker `http-stream@v0` driver flushes
per chunk). Mark `@cloudspe/livepeer-openai-gateway-core@4.0.1` as
`npm deprecate`-marked or `npm unpublish`-ed (per superseded brief
§5.7 lock; user-confirmed no external consumers). Suite shell's
`livepeer-openai-gateway` repo flips to maintenance; final release
tag points at this brief.

**Acceptance:** streaming-latency post-phase-4 ≤ 1.2× pre-phase-4
(measured at first-token); suite npm package state confirmed
deprecated; suite shell repo's `main` branch carries `DEPRECATED.md`
pointer to this monorepo.

## 14. Resolved decisions

User walks 2026-05-06 + ongoing locks; recorded as `DECIDED:` blocks.

### Q1. Rate-card schema — shell `app.*` or product `openai.*`?

**DECIDED: keep in `app.*`.** Plan 0013-shell §7 routes per-product
schemas to per-product namespaces, but the rate-card pages are
exclusively openai-admin-SPA today, and no other product gateway
references those tables. Keeping `app.rate_card_*` + `app.retail_price_*`
in the shared `app` schema avoids an admin SPA refactor without
operator benefit. Documented as a pragmatic exception. The vtuber +
video gateways ship their pricing tables in their own schemas.

### Q2. Two-package collapse vs preserve

**DECIDED: collapse.** No `-core` engine + shell split. One
component, MIT, pnpm workspace dep on `customer-portal/`. Suite's
`@cloudspe/livepeer-openai-gateway-core@4.0.1` is `npm deprecate`-marked
at phase 5.

### Q3. `/v1/audio/speech` — disposition

**DECIDED: 503 + `Livepeer-Error: mode_unsupported`** until a future
spec plan defines `http-binary-stream@v0`. No buffered-via-`http-
reqresp@v0` workaround. Behind `OPENAI_AUDIO_SPEECH_ENABLED=false`
flag (default off). Tracked in superseded brief §5.4 as Q4 lock; this
brief preserves.

### Q4. `/v1/images/generations` — port now or later?

**DECIDED: port now.** First adopter; JSON URLs and base64-inline both
fit `http-reqresp@v0`. Suite ships it; rewrite reference doesn't. The
port is mechanical.

### Q5. `x-livepeer-audio-duration-seconds` custom header

**DECIDED: retire.** Fold into `Livepeer-Work-Units`. Update
`livepeer-network-protocol/extractors/` so the audio-duration
extractor reads the canonical header. Suite's read at
`fetch.ts:200` and constant at `types/transcriptions.ts:11` are
deleted. Tracked in superseded brief §5.6 as Q4 lock; this brief
preserves.

### Q6. Streaming pass-through

**DECIDED: true pass-through, not buffered.** Suite's current path is
true streaming (`runtime/http/chat/streaming.ts`); reference's plan
0009 buffers as tracked tech debt. The migration must not regress.
Use Node's `http.request` with an unbuffered pipe to the customer
reply through `gateway-adapters/`'s `http-stream@v0` send.

### Q7. Header rename — backward compat

**DECIDED: no backward compat.** The migration phase 4 is a wire cut.
Old workers that only accept lowercase `livepeer-payment` are out;
operators run rewrite-shaped workers post-cut. HTTP is technically
case-insensitive on header *names*, but every reference impl in the
rewrite uses canonical case; we lock that.

### Q8. Tech-stack — Fastify 4 → 5 bump

**DECIDED: bump to 5 as part of the port.** Suite is on Fastify 4;
rewrite is on Fastify 5 (per `gateway-adapters/` and reference
`openai-gateway/`). Coexistence is impossible without compat shims;
the port absorbs the bump.

### Q9. `circuitBreaker.ts` — keep or drop?

**DECIDED: keep, scoped to broker-URL.** Suite's circuit-breaker
guards per-node failures. Post-collapse there's a single broker URL
per gateway, but per-broker-URL circuit-breaking is still useful
(prevents tight retry loops on broker outage). Port from
`livepeer-openai-gateway-core/src/service/routing/circuitBreaker.ts`.

### Q10. Suite `@cloudspe/livepeer-openai-gateway-core@4.0.1` package

**DECIDED: deprecate or unpublish.** No known external consumers (per
superseded brief §5.7 lock). Choose `npm deprecate` if the registry
permits; `npm unpublish` only if within the 72h window. Coordination
concern, not a technical blocker.

### OQ1. Default `mock-runner` Docker image

**DECIDED: ship `mock-runner` as a sibling Docker image** at
`openai-gateway/test/mock-runner/`. FastAPI shim returning canned
vLLM-shaped responses for chat / embeddings / transcriptions / speech
/ images; lets `make smoke` work without external Ollama / vLLM / GPU
dependencies. Mirrors how the conformance runner already bakes in
fixtures + ffmpeg for offline use. Image tag tracks the gateway's tag
(no separate version axis).

### OQ2. `OPENAI_DEFAULT_OFFERING_PER_CAPABILITY` shape

**DECIDED: YAML on disk** at `/etc/openai-gateway/offerings.yaml`
(operator mounts read-only). Operator-friendly: multi-line, comments,
version-control diffable. Env-var JSON is tighter but harder to
maintain when the offering catalog grows. Schema documented in
`openai-gateway/docs/operator-runbook.md` §"Per-capability default
offering". See §10.2 for the sample shape.

### OQ3. `images-generations` adoption phase

**DECIDED: phase 4 (with the wire cut).** Cleaner sequencing — the
rate-card scaffolding lands in phase 2 generically; images-specific
routes wire alongside the broker-cut. Phase 2 (pricing port) leaves
images metadata for the catalog but does NOT add the OpenAI route or
the broker dispatch; phase 4 ships both together so customer-facing
image-generation lights up in one cut.

### OQ4. `Livepeer-Request-Id` emission policy

**DECIDED: always emit.** Suite shape — gateway always synthesises a
UUID per request (callerId-derived, or fresh `crypto.randomUUID()`).
Helps operator debugging across the broker hop. Customers who want to
trace can override by supplying their own `Livepeer-Request-Id` header
(gateway respects the customer-supplied value when present).

## 15. Out of scope (forwarding addresses)

- **`http-binary-stream@v0` mode definition** — separate spec plan
  (`livepeer-network-protocol/modes/`); until ships,
  `/v1/audio/speech` is 503'd.
- **`openai-worker-node` Go process** — replaced by
  `capability-broker/` + workload binaries. See
  `0013-runners-byoc-migration.md`.
- **Customer-portal shell internals** — plan 0013-shell.
- **Wire-protocol middleware for non-HTTP modes** — plan 0008-followup.
- **Chain integration** — plan 0016 is the chain gate; this brief's
  phase 4 implementation runs on stubs until 0016 closes.
- **Sender daemon hot-key handling** — plan 0017.
- **Cutover plan + customer comms** — operations runbook, not this
  paper.
- **`livepeer-byoc/gateway-proxy/`** — not migrated (was for
  go-livepeer; not needed in rewrite).
- **`livepeer-byoc/register-capabilities/`** — replaced by
  orch-coordinator scrape per plan 0018; runners' `GET /options`
  endpoint preserved.
- **`livepeer-byoc/deployment-examples/`** — not migrated.

---

## Appendix A — file paths cited

This monorepo:

- `docs/exec-plans/superseded/0013-openai-pre-collapse.md` — predecessor.
- `docs/exec-plans/active/0013-shell-customer-portal-extraction.md`
  — foundation.
- `docs/exec-plans/active/0008-followup-gateway-adapters-non-http-modes.md`
  — adjacent layer.
- `docs/exec-plans/completed/0008-gateway-adapters-typescript-middleware.md`
  — adjacent layer.
- `docs/exec-plans/completed/0009-openai-gateway-reference.md` — predecessor.
- `docs/exec-plans/completed/0014-wire-compat-and-sender-daemon.md`
  — wire-compat lock.
- `docs/exec-plans/active/0016-chain-integrated-payment-design.md`
  — chain gate.
- `livepeer-network-protocol/headers/livepeer-headers.md` — header set.
- `livepeer-network-protocol/proto/livepeer/payments/v1/payer_daemon.proto:43`
  — `CreatePayment` RPC.
- `livepeer-network-protocol/proto/livepeer/payments/v1/types.proto:117-135`
  — `Payment` envelope shape.
- `openai-gateway/src/livepeer/headers.ts:5-15` — reference emission site.
- `openai-gateway/src/livepeer/payment.ts:64` — `DEFAULT_FACE_VALUE_WEI`.
- `openai-gateway/src/routes/chat-completions.ts:38-40` — buffered SSE
  tech-debt note (must not regress in collapse).

Suite paths cited (no port; reference only — see §5 for the actual map):

- `livepeer-network-suite/livepeer-openai-gateway-core/src/runtime/http/{chat,embeddings,images,audio}/...`
- `livepeer-network-suite/livepeer-openai-gateway-core/src/dispatch/{chatCompletion,streamingChatCompletion,embeddings,images,speech,transcriptions}.ts`
- `livepeer-network-suite/livepeer-openai-gateway-core/src/providers/{nodeClient,payerDaemon,serviceRegistry}.ts`
- `livepeer-network-suite/livepeer-openai-gateway-core/src/service/{payments,pricing,tokenAudit,routing,billing}/...`
- `livepeer-network-suite/livepeer-openai-gateway-core/src/types/capability.ts:14-21`
- `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/main.ts:32-64`
- `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/runtime/http/{account,admin,billing,portal,stripe}/...`
- `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/src/service/{auth,billing,pricing,admin}/...`
- `livepeer-network-suite/livepeer-openai-gateway/packages/livepeer-openai-gateway/migrations/{0000_app_init,0001_rate_card,0002_seed_rate_card,0003_idempotency_requests,0004_retail_pricing}.sql`
- `livepeer-network-suite/livepeer-openai-gateway/frontend/{portal,admin,shared}/...`
- `livepeer-network-suite/livepeer-openai-gateway/Dockerfile` + `compose.yaml`.
