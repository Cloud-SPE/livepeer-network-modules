---
plan: 0013-video
title: video-gateway absorbs suite shell + video-core engine — design
status: design-doc
phase: plan-only
opened: 2026-05-07
owner: harness
related:
  - "active plan 0013-shell — customer-portal/ shared SaaS shell library (foundation)"
  - "active plan 0013-openai — sibling product collapse (precedent shape)"
  - "active plan 0013-vtuber — sibling product collapse"
  - "completed plan 0011 — rtmp-ingress-hls-egress driver (broker mode, session-open)"
  - "completed plan 0011-followup — rtmp-ingress-hls-egress media pipeline"
  - "active plan 0008-followup — gateway-adapters non-HTTP modes (RTMP middleware)"
  - "active plan 0016 — chain-integrated payment-daemon (chain gate)"
  - "design-doc docs/design-docs/migration-from-suite.md"
audience: video-gateway maintainers planning the suite shell + video-core engine absorption
---

# Plan 0013-video — video-gateway absorbs shell + engine (design)

> **Paper-only design brief.** No code, no `package.json`, no
> migrations ship from this commit. Locks recorded in §14 as
> `DECIDED:` blocks; user walks remaining open questions before
> implementation.

## 1. Status and scope

Scope: **the video product family** absorbs into one rewrite component
`video-gateway/`. Two suite repos collapse:

1. `livepeer-network-suite/livepeer-video-gateway/` — Cloud-SPE shell
   (apps/api Fastify gateway + web-ui admin SPA + apps/playback-origin
   stub). Customer-facing video upload + live-streaming SaaS.
2. `livepeer-network-suite/livepeer-video-core/` — engine
   (`@cloudspe/video-core@0.0.1-dev`); framework-free dispatchers +
   adapter contracts + service-layer pure functions (cost quoter,
   encoding planner, manifest builder, webhook signer, playback URL
   builder).

The collapse mirrors `0013-openai` exactly: one OSS-MIT package
(`video-gateway/`), depends on `customer-portal/` for SaaS surfaces,
depends on `gateway-adapters/` for wire protocol middleware (HTTP
modes for VOD; `rtmp-ingress-hls-egress@v0` for live).

Chain-gating: this brief **emits payments**, gates on plan 0016
(chain-integrated payment-daemon) for production cutover. Pre-1.0.0
implementation against stub providers fine for paper + smoke.

Out of scope:

- The orchestrator-side `video-worker-node` Go binary — replaced by
  `capability-broker/` per
  `docs/design-docs/migration-from-suite.md` §2.
- Spec-level mode definitions — `rtmp-ingress-hls-egress@v0` frozen
  per plan 0011 + 0011-followup.
- Customer-portal shell internals — plan 0013-shell.
- Wire-protocol middleware — `gateway-adapters/` (plans 0008 +
  0008-followup).
- Workload runners (transcode-runner, abr-runner) — plan
  0013-runners.

The collapsed component ships under canonical lock (Fastify 5 + Zod
3 + drizzle-orm + ESM TS 5 + Node 20+ + pnpm + Postgres 16 + Redis 7
+ Stripe 14 + Lit 3 + RxJS 7), MIT-licensed.

## 2. What predecessor work left unfinished

The video product is on the same OSS-vs-SaaS dual-package path as
the openai product (engine + shell separation), but ships with two
extra surfaces the openai shell does not have:

- **Live streams** (plan 0011 + 0011-followup) — RTMP ingest +
  LL-HLS egress. Plan 0011 closes the broker side; this brief
  closes the gateway-shell side.
- **VOD pipeline** — video-core's `dispatch/uploadCreate.ts` +
  `dispatch/vodSubmit.ts` + `dispatch/vodStatus.ts` define an
  upload + transcode + delivery pipeline (asset → encoding_jobs
  → renditions → playback_ids).

Plan 0011-followup ships the broker-side RTMP listener, FFmpeg
profiles, LL-HLS server, lifetime management. The video-gateway
needs a parallel customer-side adapter: terminate customer auth
(API keys, mTLS, optional AuthWebhookURL-style integration), proxy
plaintext RTMP to the broker per plan 0011-followup §3, surface
session-open + termination over a customer REST API. **Plan
0008-followup** (parallel impl) extends `gateway-adapters/` to
non-HTTP modes; the video-gateway is the first consumer of
`session-control-plus-media@v0` + `rtmp-ingress-hls-egress@v0`
adapter middleware once 0008-followup closes.

The suite's `livepeer-video-core@0.0.1-dev` is **alpha**, never
published externally. The collapse retires the package outright.

## 3. Reference architecture

```
   customer encoder (OBS, ffmpeg, mux/twitch-style push)
     │  rtmp://gateway:1935/<stream_key>
     ▼
   ┌───────────────────────────────────────────────────────┐
   │  video-gateway/                                       │
   │   - SaaS surfaces (customer-portal/-delegated):       │
   │       portal: projects / API keys / live streams /    │
   │              VOD assets / playback / billing          │
   │       admin:  customers / topups / nodes / pricing    │
   │   - product routes (Fastify):                         │
   │       POST   /v1/live/streams                         │
   │       POST   /v1/live/streams/:id/end                 │
   │       GET    /v1/live/streams/:id                     │
   │       POST   /v1/uploads        (resumable upload)    │
   │       POST   /v1/vod/submit     (encoding-job submit) │
   │       GET    /v1/vod/:asset_id  (status)              │
   │       GET    /v1/playback/:id   (playback URL builder)│
   │       POST   /v1/projects                             │
   │   - RTMP ingress adapter:                             │
   │       :1935 RTMP listener (customer-facing)           │
   │       proxies plaintext RTMP → broker:1935 with       │
   │       <session_id>/<stream_key> path injection per     │
   │       plan 0011-followup §4.2                         │
   │   - playback origin (HLS playlist+segment proxy):     │
   │       /_hls/<session>/...  (strict pass-through to    │
   │       broker /_hls/...; no rewrite layer — see §14    │
   │       OQ1 lock; CDN concerns are operator add-on)     │
   └───────────────────────────────────────────────────────┘
       │                                          ▲
       │ HTTPS to capability-broker for VOD jobs  │ HLS playback
       │ + live-stream session-open               │
       ▼                                          │
   ┌───────────────────────────────────────────────────────┐
   │  capability-broker (orch host)                        │
   │   modes: rtmp-ingress-hls-egress@v0 (live)            │
   │          http-reqresp@v0 (VOD job submit)             │
   │          http-stream@v0 (VOD status long-poll)        │
   └───────────────────────────────────────────────────────┘
       │
       ▼
   ┌───────────────────────────────────────────────────────┐
   │  workload runners (per 0013-runners):                 │
   │    transcode-runner (VOD)                             │
   │    abr-runner (VOD multi-rendition)                   │
   │    (live-transcode-runner: skipped per plan 0011-fu)  │
   └───────────────────────────────────────────────────────┘
```

Wire layers: the gateway emits the canonical Livepeer header set per
`livepeer-network-protocol/headers/livepeer-headers.md`. Modes used:

- `rtmp-ingress-hls-egress@v0` — live streams; session-open + RTMP
  listen + LL-HLS egress.
- `http-reqresp@v0` — VOD job submit (asset → encoded outputs).
- `http-stream@v0` — VOD status long-poll if used (encoding-job
  progress) — though job status via direct DB read is simpler; see §4.

## 4. Component layout

```
video-gateway/
  AGENTS.md
  DESIGN.md
  README.md
  Makefile                       ← `make build|test|lint|smoke|migrate`
  Dockerfile                     ← multi-stage; node + RTMP listener
  compose.yaml                   ← extends rewrite reference
  compose.prod.yaml
  package.json                   ← @livepeer-network-modules/video-gateway
  tsconfig.json
  vitest.config.ts
  drizzle.config.ts
  migrations/
    0000_video_init.sql          ← media.assets / encoding_jobs / live_streams / playback_ids / renditions / uploads
    0001_pricing.sql             ← media.pricing tables (per-minute live, per-resolution VOD)
    0002_live_session_debits.sql ← media.live_session_debits ledger
    0003_webhook_endpoints.sql   ← media.webhook_endpoints + media.webhook_deliveries
  src/
    main.ts                      ← composition root
    config.ts                    ← Zod env (video-specific)
    server.ts                    ← Fastify factory; ALSO RTMP listener factory
    livepeer/                    ← wire layer
      headers.ts
      payment.ts
      rtmp-adapter.ts            ← customer-facing :1935; proxies to broker
    routes/
      live-streams.ts            ← /v1/live/streams
      vod-submit.ts              ← /v1/vod/submit
      vod-status.ts              ← /v1/vod/:asset_id
      uploads.ts                 ← /v1/uploads (resumable; tus-js-client compat)
      playback.ts                ← /v1/playback/:id
      projects.ts                ← /v1/projects
      webhooks.ts                ← /v1/webhooks (configure customer-side delivery URLs)
      account.ts                 ← shell delegator
      admin.ts                   ← shell delegator + product extras
      billing-topup.ts           ← shell delegator
      stripe-webhook.ts          ← shell delegator
    pricing/
      videoRateCard.ts           ← per-minute live + per-resolution VOD pricing
    repo/
      assets.ts
      encodingJobs.ts
      liveStreams.ts
      playbackIds.ts
      renditions.ts
      uploads.ts
      webhookEndpoints.ts
      webhookDeliveries.ts
      videoUsageRecords.ts
    service/
      live/
        liveSessionService.ts    ← session-open + close
        liveRouteSelector.ts     ← live-stream → broker session mapping
        liveWorkerSessionClient.ts
        recordingBridge.ts       ← (renamed: recording bridge → recording-egress)
        staleStreamSweeper.ts    ← stuck-session sweep
        postgresAdapters.ts      ← drizzle-side adapters
      vod/
        encodingPlanner.ts       ← from video-core
        manifestBuilder.ts       ← from video-core
        playbackUrlBuilder.ts    ← from video-core
        jobOrchestrator.ts       ← from video-core
        costQuoter.ts            ← from video-core
        webhookSigner.ts         ← from video-core (HMAC-signed customer webhooks)
      auth/
        keyHasher.ts             ← (kept; adopts shell pattern)
        keyMinter.ts             ← (kept; adopts shell pattern)
      billing/
        prepaidWallet.ts         ← thin adapter onto customer-portal Wallet
      health/
        healthChecker.ts
    runtime/
      rtmp/
        listener.ts              ← yutopp/go-rtmp-style listener (TS — see §6.2 variance)
        proxy.ts                 ← proxies plaintext RTMP → broker :1935
        keyParser.ts             ← parse <session_id>/<stream_key> from publish path
    frontend/
      portal/
        components/
          portal-projects.ts
          portal-live-streams.ts
          portal-vod-assets.ts
          portal-playback.ts
          portal-webhooks.ts
        index.html
      admin/
        components/
          admin-projects.ts
          admin-pricing-live.ts
          admin-pricing-vod.ts
          admin-nodes.ts
          admin-api-keys.ts
        index.html
  test/
    integration/
    smoke/
```

The `apps/playback-origin/` from the suite (a near-empty package
today; just `package.json` + README) is **dropped**. The gateway's
own `/_hls/<session>/...` route proxies the broker's HLS server per
plan 0011-followup §6.3.

## 5. Source-to-destination file map

### 5.1 video-core engine → video-gateway/src/

| Source | Target |
|---|---|
| `livepeer-network-suite/livepeer-video-core/src/index.ts:1-30` (public barrel) | folded into `video-gateway/src/index.ts` |
| `livepeer-network-suite/livepeer-video-core/src/dispatch/abr.ts` | `video-gateway/src/service/vod/abr.ts` |
| `livepeer-network-suite/livepeer-video-core/src/dispatch/liveStream.ts` | `video-gateway/src/service/live/liveStreamDispatch.ts` (folded into `liveSessionService.ts` if small) |
| `livepeer-network-suite/livepeer-video-core/src/dispatch/playbackResolve.ts` | `video-gateway/src/service/vod/playbackResolve.ts` |
| `livepeer-network-suite/livepeer-video-core/src/dispatch/uploadCreate.ts` | `video-gateway/src/service/vod/uploadCreate.ts` |
| `livepeer-network-suite/livepeer-video-core/src/dispatch/uploadStatus.ts` | `video-gateway/src/service/vod/uploadStatus.ts` |
| `livepeer-network-suite/livepeer-video-core/src/dispatch/vodSubmit.ts` | `video-gateway/src/service/vod/vodSubmit.ts` |
| `livepeer-network-suite/livepeer-video-core/src/dispatch/vodStatus.ts` | `video-gateway/src/service/vod/vodStatus.ts` |
| `livepeer-network-suite/livepeer-video-core/src/dispatch/types.ts` | `video-gateway/src/service/vod/types.ts` |
| `livepeer-network-suite/livepeer-video-core/src/service/costQuoter.ts` | `video-gateway/src/service/vod/costQuoter.ts` |
| `livepeer-network-suite/livepeer-video-core/src/service/encodingPlanner.ts` | `video-gateway/src/service/vod/encodingPlanner.ts` |
| `livepeer-network-suite/livepeer-video-core/src/service/jobOrchestrator.ts` | `video-gateway/src/service/vod/jobOrchestrator.ts` |
| `livepeer-network-suite/livepeer-video-core/src/service/manifestBuilder.ts` | `video-gateway/src/service/vod/manifestBuilder.ts` |
| `livepeer-network-suite/livepeer-video-core/src/service/playbackUrlBuilder.ts` | `video-gateway/src/service/vod/playbackUrlBuilder.ts` |
| `livepeer-network-suite/livepeer-video-core/src/service/webhookSigner.ts` | `video-gateway/src/service/vod/webhookSigner.ts` |
| `livepeer-network-suite/livepeer-video-core/src/providers/consoleLogger.ts` | covered by `customer-portal/` logger; or local trivial impl |
| `livepeer-network-suite/livepeer-video-core/src/providers/inMemoryRateLimiter.ts` | `video-gateway/src/testing/rateLimiterFakes.ts` |
| `livepeer-network-suite/livepeer-video-core/src/providers/noopWebhookSink.ts` | `video-gateway/src/testing/webhookFakes.ts` |
| `livepeer-network-suite/livepeer-video-core/src/types/...` | `video-gateway/src/types/...` (verbatim) |
| `livepeer-network-suite/livepeer-video-core/src/interfaces/...` (adapter contracts) | folded into `video-gateway/src/service/...` interfaces (no separate `interfaces/` barrel; contracts ship beside their consumers) |
| `livepeer-network-suite/livepeer-video-core/src/repo/...` | covered by `video-gateway/src/repo/...` (drizzle-backed) + shell repo (customer-portal/) for `app.*` tables |
| `livepeer-network-suite/livepeer-video-core/src/config/...` | folded into `video-gateway/src/config.ts` (Zod) |
| `livepeer-network-suite/livepeer-video-core/src/adapters/fastify/` | folded into `video-gateway/src/server.ts` |
| `livepeer-network-suite/livepeer-video-core/src/dashboard/` | dropped (customer-portal/ admin SPA replaces) |
| `livepeer-network-suite/livepeer-video-core/src/testing/` | `video-gateway/src/testing/` (verbatim) |
| `livepeer-network-suite/livepeer-video-core/examples/{minimal-shell,wallets}/` | dropped (post-collapse, the gateway *is* the example) |

### 5.2 video-gateway shell → video-gateway/src/

| Source | Target |
|---|---|
| `livepeer-network-suite/livepeer-video-gateway/apps/api/src/main.ts` | `video-gateway/src/main.ts` |
| `livepeer-network-suite/livepeer-video-gateway/apps/api/src/runtime/http/admin/{health,index,repoTypes}.ts` | `video-gateway/src/routes/admin.ts` (delegator + product extras) |
| `livepeer-network-suite/livepeer-video-gateway/apps/api/src/runtime/http/projects/index.ts` | `video-gateway/src/routes/projects.ts` |
| `livepeer-network-suite/livepeer-video-gateway/apps/api/src/runtime/http/webhooks/index.ts` | `video-gateway/src/routes/webhooks.ts` |
| `livepeer-network-suite/livepeer-video-gateway/apps/api/src/runtime/http/internal/live/{index,index.test}.ts` | `video-gateway/src/routes/live-streams.ts` |
| `livepeer-network-suite/livepeer-video-gateway/apps/api/src/service/auth/{keyHasher,keyMinter}.ts` | folded against `customer-portal/src/auth/apiKey.ts` (shell version is canonical; suite version retires) |
| `livepeer-network-suite/livepeer-video-gateway/apps/api/src/service/billing/prepaidWallet.ts` | `video-gateway/src/service/billing/prepaidWallet.ts` (thin adapter onto shell wallet) |
| `livepeer-network-suite/livepeer-video-gateway/apps/api/src/service/healthChecker.ts` | `video-gateway/src/service/health/healthChecker.ts` |
| `livepeer-network-suite/livepeer-video-gateway/apps/api/src/service/live/*` (8 files) | `video-gateway/src/service/live/*` |
| `livepeer-network-suite/livepeer-video-gateway/apps/api/src/service/pricing/*` | `video-gateway/src/pricing/videoRateCard.ts` |
| `livepeer-network-suite/livepeer-video-gateway/apps/api/src/service/webhook/*` | folded into `video-gateway/src/service/vod/webhookSigner.ts` |
| `livepeer-network-suite/livepeer-video-gateway/apps/api/src/repo/{assetRepo,encodingJobRouteRepo,jobRepo,liveStreamRepo,playbackIdRepo,uploadRepo,shellRepos,db,migrate}.ts` | `video-gateway/src/repo/*.ts` |
| `livepeer-network-suite/livepeer-video-gateway/apps/api/src/repo/schema/{app,media}/index.ts` | `video-gateway/src/db/schema.ts` (split per-namespace) |
| `livepeer-network-suite/livepeer-video-gateway/apps/api/drizzle/0000_initial_schema.sql` | `video-gateway/migrations/0000_video_init.sql` (the `media.*` tables; `app.*` tables retire to customer-portal/) |
| `livepeer-network-suite/livepeer-video-gateway/apps/api/drizzle/0001_encoding_job_routes.sql` | `video-gateway/migrations/0000_video_init.sql` (folded) |
| `livepeer-network-suite/livepeer-video-gateway/apps/api/drizzle/0002_assets_selected_offering.sql` | `video-gateway/migrations/0000_video_init.sql` (folded) |
| `livepeer-network-suite/livepeer-video-gateway/apps/api/drizzle/0003_live_stream_pattern_b_fields.sql` | `video-gateway/migrations/0000_video_init.sql` (folded) |
| `livepeer-network-suite/livepeer-video-gateway/apps/api/Dockerfile` | `video-gateway/Dockerfile` |
| `livepeer-network-suite/livepeer-video-gateway/web-ui/src/components/admin-{api-keys,app,audit,health,login,nodes,pricing,projects}.ts` | `video-gateway/src/frontend/admin/components/admin-*.ts` (TS already; not all retained — `audit`/`health` come from customer-portal/) |
| `livepeer-network-suite/livepeer-video-gateway/web-ui/src/services/*` | folded into the customer-portal/ shared lib + product-specific service classes in `video-gateway/src/frontend/admin/lib/` |
| `livepeer-network-suite/livepeer-video-gateway/web-ui/src/main.ts` + `index.html` + Vite config | `video-gateway/src/frontend/admin/{main.ts,index.html,vite.config.ts}` |
| `livepeer-network-suite/livepeer-video-gateway/apps/playback-origin/` | **dropped** (gateway proxies broker LL-HLS; no separate origin) |
| `livepeer-network-suite/livepeer-video-gateway/packages/shared/` | folded into `video-gateway/src/types/` (cross-app types) |
| `livepeer-network-suite/livepeer-video-gateway/Makefile` | `video-gateway/Makefile` |
| `livepeer-network-suite/livepeer-video-gateway/infra/` | folded into `video-gateway/{compose.yaml,compose.prod.yaml}` |

### 5.3 Customer RTMP adapter (NEW)

The video-gateway is the customer-facing RTMP termination point per
plan 0011-followup §3 ("gateway RTMP adapter (plan 0008-followup,
parallel)"). Plan 0008-followup will deliver the `gateway-adapters/`
RTMP middleware; the video-gateway is the first consumer.

| New file | Purpose |
|---|---|
| `video-gateway/src/runtime/rtmp/listener.ts` | yutopp/go-rtmp-style RTMP listener; native Node + a TS RTMP lib (see §6.2) |
| `video-gateway/src/runtime/rtmp/proxy.ts` | After authn, proxies plaintext RTMP bytes to broker:1935 with the broker's `<session_id>/<stream_key>` path injection |
| `video-gateway/src/runtime/rtmp/keyParser.ts` | Parse the customer's RTMP `PublishingName` → API-key + stream-key (or just stream-key if API-key authn'd at TLS layer) |
| `video-gateway/src/livepeer/rtmp-adapter.ts` | Bridges between customer auth (API key) and the broker's session-open POST `/v1/cap` flow per plan 0011-followup §4.2 |

## 6. Tech-stack lock + variance justification

### 6.1 Canonical lock

Same as `customer-portal/` and `openai-gateway/`. Fastify 5 + Zod 3 +
drizzle-orm + ESM TS 5 + Node 20+ + pnpm + Postgres 16 + Redis 7 +
Stripe 14 + Lit 3 + RxJS 7. MIT.

### 6.2 Variance: TS RTMP listener vs native

Plan 0011-followup §4.5 locked the broker's RTMP library at
`github.com/yutopp/go-rtmp` (Go). The video-gateway's customer-side
RTMP listener is **TypeScript** (the gateway is TS); options:

(a) Pure-TS RTMP lib (e.g. `rtmp-relay`, `node-media-server`).
(b) Spawn a small Go `rtmp-proxy` sidecar binary using yutopp/go-rtmp,
    same as broker.

**DECIDED Q1 below: (a) pure-TS.** Justification: the TS RTMP libs
are mature enough for plaintext-RTMP proxy duty (the gateway's job is
simpler than the broker's — it doesn't transcode, just forwards
bytes after authn). Sidecar (b) would force a second runtime in the
gateway image. Reassess if a TS lib turns out unstable in production.

This is **not** a stack variance from the canonical lock; it's a
library choice within the canonical lock. Documented because RTMP +
TS is unusual.

### 6.3 No Python / no browser-TS variance

The video-gateway is pure Node + Lit. No Python (the workload runners
are byoc-side). No browser variance beyond Lit (already canonical for
shell SPAs).

### 6.4 Canonical schema namespace

Per plan 0013-shell Q6 + 0013-openai Q1 patterns: `media.*` namespace
for video-gateway product schema (matches the suite shell's existing
namespace at `livepeer-network-suite/livepeer-video-gateway/apps/api/drizzle/0000_initial_schema.sql:1`).
The shell `app.*` schema is imported via `customer-portal/`.

## 7. DB schema

### 7.1 Customer-portal-owned (`app.*`)

Per plan 0013-shell §7. Six tables: customers, api_keys, reservations,
topups, stripe_webhook_events, admin_audit_events,
idempotency_requests.

### 7.2 Video-gateway-owned (`media.*`)

| Table | Purpose | Source |
|---|---|---|
| `media.assets` | One row per VOD asset; status, source url, duration, codecs, ffprobe metadata. Includes `deleted_at TIMESTAMP NULL` for soft-delete (§14 OQ2 lock). | suite migration `0000_initial_schema.sql:5-25` |
| `media.encoding_jobs` | One row per transcode/abr job; FK asset_id; worker_url, attempt_count, input/output urls. | suite migration `0000_initial_schema.sql:27-40` + `0001_encoding_job_routes.sql` |
| `media.live_streams` | Live-stream sessions; FK project_id; selected_capability, selected_offering, payment_work_id, terminal_reason. | suite migration `0000_initial_schema.sql` + `0003_live_stream_pattern_b_fields.sql:1-26` |
| `media.playback_ids` | Per-asset/per-stream playback tokens. | suite migration `0000_initial_schema.sql` |
| `media.renditions` | Per-asset rendition metadata (resolution, bitrate, codec). | suite migration `0000_initial_schema.sql` |
| `media.uploads` | Resumable upload records (tus protocol). | suite migration `0000_initial_schema.sql` |
| `media.live_session_debits` | Per-second debit ledger for live streams. | suite migration `0000_initial_schema.sql` (in `app.*` originally; moves to `media.*` for cohesion) |
| `media.pricing_live` | Per-resolution per-minute pricing. | suite migration `0000_initial_schema.sql` (was `app.retail_pricing`; product-specific) |
| `media.pricing_vod` | Per-resolution per-minute encoding pricing. | suite migration `0000_initial_schema.sql` (was `app.retail_pricing`; product-specific) |
| `media.usage_records` | Per-job usage rollups. | suite migration `0000_initial_schema.sql` |
| `media.webhook_endpoints` | Customer-configured outbound webhook URLs. | suite migration `0000_initial_schema.sql` |
| `media.webhook_deliveries` | Per-attempt delivery log (signed; HMAC). | suite migration `0000_initial_schema.sql` |
| `media.projects` | Video-product project namespacing (separate from "customer"). | suite migration `0000_initial_schema.sql` (was `app.projects`; moves to `media.*`) |

The suite mixes `app.*` and `media.*` (suite migration creates both
schemas in the same SQL). The rewrite splits the namespace ownership
strictly: `customer-portal/` owns `app.*`; `video-gateway/` owns
`media.*`. Suite's `app.api_keys` / `app.balances` /
`app.reservations` / `app.audit_events` retire to `customer-portal/`.
Suite's `app.live_session_debits` / `app.projects` /
`app.retail_pricing` / `app.usage_records` / `app.webhook_*` move to
`media.*` (product-specific). Suite's `app.users` is replaced by
`customer-portal/`'s `app.customers`.

Migrations: linear within each component; `customer-portal/migrations/`
runs first, then `video-gateway/migrations/`. The shell's
`runMigrations(db, dir)` helper called twice from
`video-gateway/src/main.ts`.

## 8. Customer-facing surfaces

### 8.1 UI flows

`portal-*` (customer-portal/-shared): login, dashboard, API keys,
top-up, settings.

`portal-*` (video-gateway/-product-specific):
- `portal-projects` — project list / create / delete.
- `portal-live-streams` — live stream list; New Stream → returns
  RTMP push URL + playback URL.
- `portal-vod-assets` — uploads + transcode status + playback URL.
- `portal-playback` — embed-player code / playback URL helper.
- `portal-webhooks` — configure outbound webhook URLs + delivery log.

`admin-*` (customer-portal/-shared): customers / topups / audit /
nodes / reservations.

`admin-*` (video-gateway/-product-specific):
- `admin-projects` — operator view of projects.
- `admin-pricing-live` — per-minute live pricing per resolution.
- `admin-pricing-vod` — per-minute encoding pricing per resolution.

### 8.2 API endpoints

| Method + path | Mode | Capability | Notes |
|---|---|---|---|
| `POST /v1/live/streams` | `rtmp-ingress-hls-egress@v0` | `livepeer:live-stream` | Returns `{stream_id, rtmp_push_url, stream_key, hls_playback_url, expires_at}`. The push URL points at the gateway's :1935; the HLS URL points at the gateway's `/_hls/...` proxy. |
| `GET /v1/live/streams/:id` | (REST) | n/a | Status (`starting/active/ended/errored`) + per-second cost accrued. |
| `POST /v1/live/streams/:id/end` | (REST) | n/a | Customer kill. |
| `POST /v1/uploads` | (tus protocol) | n/a | Resumable upload (chunks). On completion → emit `vod-submit` automatically per project policy. |
| `POST /v1/vod/submit` | `http-reqresp@v0` | `livepeer:vod-transcode` | Submit asset_id for transcode; broker dispatches to transcode-runner / abr-runner. |
| `GET /v1/vod/:asset_id` | (REST or `http-stream@v0` long-poll) | n/a | Job status + rendition URLs. |
| `GET /v1/playback/:id` | (REST) | n/a | Resolves playback_id to playable URL (HLS for live, MP4/HLS for VOD). |
| `GET /_hls/<session>/...` | (HLS pass-through) | n/a | Strict proxy to broker `/_hls/...` per §14 OQ1; no header rewriting / no cache-control / no CORS injection at gateway. CDN is operator add-on. |
| `GET /v1/videos/assets` | (REST) | n/a | List VOD assets; soft-deleted rows hidden unless `?include_deleted=true`. |
| `GET /v1/videos/assets/{id}` | (REST) | n/a | Returns 404 on soft-deleted asset unless `?include_deleted=true`. |
| `DELETE /v1/videos/assets/{id}` | (REST) | n/a | Soft-delete: sets `media.assets.deleted_at = now()`; returns 204. Hard-delete deferred to retention janitor (§14 OQ2). |
| `POST /v1/projects` + `GET /v1/projects` + `POST /v1/projects/:id/...` | (REST) | n/a | Project CRUD. |
| `POST /v1/webhooks` + `GET /v1/webhooks` | (REST) | n/a | Customer-side webhook URL configuration. |
| `RTMP rtmp://gateway:1935/<stream_key>` | (RTMP wire) | n/a | Customer encoder push; gateway authn-and-proxy to broker per plan 0011-followup §4.2. |

### 8.3 Header set per outbound call

Same as `0013-openai` §8.3. The video-gateway emits all six headers
(`Livepeer-Capability`, `Livepeer-Offering`, `Livepeer-Payment`,
`Livepeer-Spec-Version`, `Livepeer-Mode`, `Livepeer-Request-Id`).

For RTMP ingest the headers are HTTP-level on the session-open
POST; the RTMP byte stream itself carries no Livepeer headers (RTMP
uses TCP control messages, not HTTP).

### 8.4 OAuth, chat workers, egress workers

OAuth: same as 0013-openai (not in v0.1).

Chat workers: not applicable.

Egress workers: the gateway's `playback-origin` proxy is the egress
surface for customer playback. The broker's LL-HLS server is what's
actually serving segments; the gateway's `/_hls/<session>/...`
**strictly passes through** to it (for hostname uniformity with the
customer's stream-create response). Per §14 OQ1 lock the gateway has
**no** cache-control / CORS / playlist-rewrite logic; broker's LL-HLS
handler (plan 0011-followup §6.3) is the canonical source. Operators
front the gateway with CloudFront / Fastly / Cloudflare for caching,
edge replication, geographic routing — gateway doesn't reinvent CDN.

## 9. Cross-component dependencies

```
video-gateway/
  package.json:
    dependencies:
      "@livepeer-network-modules/customer-portal":  "workspace:*"
      "@livepeer-network-modules/gateway-adapters": "workspace:*"
      fastify, @fastify/multipart, zod, drizzle-orm, pg, ioredis,
      stripe, lit, rxjs, "<TS-RTMP-lib>" (per §6.2)
```

Same shell + adapter dependency shape as 0013-openai. Adds the TS
RTMP lib for the customer-side listener.

## 10. Configuration surface

In addition to `customer-portal/` env (plan 0013-shell §10) and the
per-`gateway-adapters/` modes:

| Env var | Required | Purpose |
|---|---|---|
| `LIVEPEER_BROKER_URL` | yes | Broker base URL. |
| `LIVEPEER_BROKER_RTMP_HOST` | yes | Broker host:1935 endpoint (proxy target). |
| `LIVEPEER_PAYER_DAEMON_SOCKET` | yes | Sender daemon socket. |
| `LIVEPEER_PAYER_DEFAULT_FACE_VALUE_WEI` | yes | Default ticket size. |
| `VIDEO_LIVE_RTMP_LISTEN_ADDR` | no (default `:1935`) | Customer-facing RTMP listener bind. |
| `VIDEO_LIVE_RTMP_TLS_CERT_PATH`, `VIDEO_LIVE_RTMP_TLS_KEY_PATH` | no | When set, listen RTMPS instead of plain RTMP. (Customer-facing TLS termination per plan 0011-followup §4.4.) |
| `VIDEO_LIVE_HLS_BASE_URL` | yes | Base URL the gateway returns for HLS playback (e.g. `https://video.example.com/_hls`). |
| `VIDEO_VOD_TUS_PATH` | no (default `/v1/uploads`) | tus upload mountpoint. |
| `VIDEO_VOD_DEFAULT_OFFERING_PER_TIER` | no (YAML) | Map customer-tier → default VOD encoding offering. |
| `VIDEO_LIVE_DEFAULT_OFFERING_PER_TIER` | no (YAML) | Map customer-tier → default live offering. |
| `VIDEO_WEBHOOK_HMAC_PEPPER` | yes | HMAC secret for outbound webhook signing. |
| `VIDEO_STALE_STREAM_SWEEP_INTERVAL_SECONDS` | no (default `60`) | staleStreamSweeper run cadence. |
| `VIDEO_GATEWAY_ABR_POLICY` | no (default `customer-tier`) | ABR ladder selection policy at session-open per §14 OQ3. v0.1 ships `customer-tier` only; future minor adds `operator-flat`. |

Note: live-stream → VOD handoff (§14 OQ4) is **not** a deployment
config — it's an **opt-in `record_to_vod: true` flag in session-open
params** (per-stream, customer-controlled, default off).

YAML config at `/etc/video-gateway/offerings.yaml` (optional):

```yaml
defaults:
  live:
    free: h264-live-1080p-passthrough
    prepaid: h264-live-1080p-nvenc
  vod:
    free: h264-vod-720p
    prepaid: h264-vod-1080p
```

## 11. Conformance / smoke tests

### 11.1 Wire-protocol conformance

Live-stream wire shape covered by
`livepeer-network-protocol/conformance/fixtures/rtmp-ingress-hls-egress/`
fixtures (plan 0011 + 0011-followup). VOD wire shape covered by
`http-reqresp@v0` + `http-stream@v0` fixtures (plan 0008).

The video-gateway's smokes consume the broker side; no new
mode-spec fixtures.

### 11.2 Component smokes

`video-gateway/test/smoke/`:
- `live-stream.smoke.ts` — POST `/v1/live/streams` → returns RTMP push
  URL; ffmpeg push-from-fixture for 5s; assert HLS playlist materializes;
  ledger debits per-second; `POST /v1/live/streams/:id/end`.
- `vod-upload-and-submit.smoke.ts` — tus upload of a small mp4 →
  `vod/submit` → broker dispatches → mock-runner returns a faked
  output URL → `vod/<asset_id>` shows status `ready`.
- `playback-resolve.smoke.ts` — GET `/v1/playback/:id` returns valid
  HLS URL; HEAD on URL returns 200.
- `webhook-delivery.smoke.ts` — configure customer webhook URL; emit a
  test event; assert HMAC-signed POST received with correct body.
- `project-isolation.smoke.ts` — same customer with two projects;
  asset in project A invisible to project B.
- `stripe-topup.smoke.ts` — same as 0013-openai.

### 11.3 Compose stack

`video-gateway/compose.yaml`:
- `postgres:16-alpine`
- `redis:7-alpine`
- `payment-daemon` (sender mode; `tztcloud/livepeer-payment-daemon:v0.8.10`)
- `capability-broker` (`tztcloud/livepeer-capability-broker:v0.8.10`)
- `mock-vod-runner` (FastAPI shim returning canned transcode results)
- `mock-rtmp-receiver` (a tiny TCP listener on :1935 that ack+drops
  for offline RTMP smoke without GPU)
- `video-gateway` (this brief)

`compose.smoke.yaml` overlay enables the mock services. For real
hardware smoke (NVENC live + transcode), operator-run on a GPU host
per plan 0011-followup §11.4.

## 12. Operator runbook deltas

`video-gateway/docs/operator-runbook.md` (NEW):

1. **Compose deploy** — env setup, RTMP listener bind, TLS cert
   mount for RTMPS (optional), HLS base URL, broker URL +
   broker-RTMP-host.
2. **Customer-facing TLS for RTMPS** — when `VIDEO_LIVE_RTMP_TLS_CERT_PATH`
   set, gateway listens RTMPS at :1936 (configurable). Document cert
   rotation cadence.
3. **Per-resolution pricing** — admin SPA `admin-pricing-live` +
   `admin-pricing-vod`; default seeded by migration.
4. **Live streams: stuck-session sweep** — `staleStreamSweeper`
   periodically cancels live streams with no RTMP activity > N
   seconds; document the threshold and how to override via env.
5. **Customer webhooks** — outbound HTTP POST signed with HMAC-SHA-256
   using `VIDEO_WEBHOOK_HMAC_PEPPER`. Customers verify via the
   `X-Livepeer-Webhook-Signature` header. Document signature scheme.
6. **VOD asset retention + soft-delete** — customer `DELETE
   /v1/videos/assets/{id}` flips `media.assets.deleted_at` and returns
   204 (per §14 OQ2). Hard-delete (broker scratch + S3 + DB row removal)
   is **forwarded to a separate janitor-job plan** — v0.1 ships
   soft-delete only. Interim manual hard-delete: operator runs
   `DELETE FROM media.assets WHERE deleted_at < NOW() - INTERVAL '30 days'`
   plus matching S3-side cleanup as their retention policy demands.
7. **HLS proxy hostname** — the gateway returns `VIDEO_LIVE_HLS_BASE_URL/_hls/<session>/...`.
   The reverse proxy must rewrite that path to broker's
   `/_hls/<session>/...`. Provide nginx + caddy snippets.
8. **Transcode-runner pairing** — the operator's broker
   `host-config.yaml` declares video transcode capabilities; the
   gateway resolves them via the broker URL.
9. **Recording bridge** — `recordingBridge.ts` (renamed; not a "bridge"
   per user-memory `feedback_no_bridge_term.md` — file renames to
   `recording-egress.ts`); customer recordings are an opt-in feature
   that pushes a parallel HLS-VOD output during live streaming.
   Document ON/OFF flag.

## 13. Migration sequence

5 phases. Each independently revertable.

### Phase 1 — Workspace scaffold + dependency wiring

`video-gateway/package.json` workspace deps on `customer-portal/` +
`gateway-adapters/`. Empty dirs for `routes/`, `service/`, `repo/`,
`pricing/`, `frontend/`, `runtime/rtmp/`, `migrations/`. Compose
stack with Postgres + Redis + payment-daemon + broker + mock-runner.

**Acceptance:** `pnpm -F @livepeer-network-modules/video-gateway build` green;
empty Fastify app starts; mock services come up.

### Phase 2 — Engine port (video-core)

Port `dispatch/`, `service/`, `types/`, `repo/`, `interfaces/`,
`testing/`, `providers/{noopWebhookSink,inMemoryRateLimiter}` from
video-core into `video-gateway/src/service/vod/` + `src/testing/`.
Drop `examples/`, `dashboard/`. Re-export public types from
`video-gateway/src/index.ts` (used internally only; no npm publish).

**Acceptance:** all engine tests green; rate-card resolver returns
canonical pricing; encodingPlanner produces deterministic output for
test fixtures. Diff: ~+2,500 LOC ported.

### Phase 3 — Shell port (apps/api)

Port `apps/api/src/{main,routes,service,repo,config}/...`. Land
`migrations/0000_video_init.sql` (folded from 4 suite migrations).
Drop `apps/api/src/repo/schema/app/` (covered by customer-portal/).
Wire `customer-portal/` middleware (auth → rate-limit → idempotency →
metrics).

**Acceptance:** integration tests against fresh DB green; admin SPA
loads against the engine; portal SPA shows projects + uploads + live
streams.

### Phase 4 — RTMP listener + customer adapter (NEW; gates on plan 0008-followup)

Wire `runtime/rtmp/{listener,proxy,keyParser}.ts` with the chosen TS
RTMP lib. Authn → proxy plaintext RTMP bytes to broker:1935 with
`<session_id>/<stream_key>` path injection per plan 0011-followup
§4.2.

**Acceptance:** live-stream.smoke.ts passes against real broker
listener (mock RTMP receiver on the way out); TLS cert + RTMPS path
verified.

### Phase 5 — Wire cut + suite shell deprecate

Same as 0013-openai phase 4: rename headers; replace
`providers/nodeClient` (if any analogue) with `gateway-adapters/`;
drop `serviceRegistry`, `quoteCache`, `quoteRefresher` (most missing
in video-core; just the shell's resolver shim retires); swap RPC
shape; re-gen suite gRPC stubs against rewrite proto. Mark
`livepeer-network-suite/livepeer-video-gateway` and
`livepeer-network-suite/livepeer-video-core` as deprecated; final
release tag points here. `@cloudspe/video-core@0.0.1-dev` was never
published; nothing to unpublish.

**Acceptance:** smoke against `capability-broker` + receiver-mode
`payment-daemon`; full live-stream lifecycle including refunds; full
VOD lifecycle including webhook delivery.

## 14. Resolved decisions

User walks 2026-05-06; recorded as `DECIDED:` blocks.

### Q1. Customer RTMP listener — pure TS or Go sidecar?

**DECIDED: pure TS.** Native Node + TS RTMP lib (e.g.
`node-media-server`, `rtmp-relay`, or hand-rolled around `node-rtmp`).
The gateway's RTMP role is plaintext-proxy after authn; doesn't
transcode; complexity stays low. Sidecar adds a second runtime to
the gateway image and an IPC boundary for no obvious win. Reassess
if production stability falters. §6.2 documents.

### Q2. apps/playback-origin — preserve or drop?

**DECIDED: drop.** Suite's playback-origin is a `package.json` +
README stub today; no logic ever shipped. The video-gateway's own
`/_hls/<session>/...` Fastify route proxies the broker's LL-HLS server.

### Q3. Schema namespace split

**DECIDED: `app.*` (shell) vs `media.*` (product), strict.** Suite
mixes both schemas in one migration; rewrite splits ownership.
Suite's `app.users` retires (replaced by `customer-portal/`'s
`app.customers`); `app.api_keys` / `balances` / `reservations` /
`audit_events` move to shell ownership; product-specific
`live_session_debits` / `projects` / `retail_pricing` /
`webhook_*` / `usage_records` move from `app.*` to `media.*`.

### Q4. video-core engine package — preserve as published artifact?

**DECIDED: drop.** `@cloudspe/video-core@0.0.1-dev` was never
published externally. Code merges into the gateway component. No
`npm deprecate` step needed.

### Q5. RTMPS at customer boundary

**DECIDED: optional, off by default.** When
`VIDEO_LIVE_RTMP_TLS_CERT_PATH` is set, the gateway listens RTMPS
on `:1936`. Plain RTMP at `:1935` always available for unencrypted
push (most encoders default plaintext). Per plan 0011-followup §4.4
locks broker-side plaintext only; customer-side TLS termination is
the gateway's job.

### Q6. tus protocol for resumable uploads

**DECIDED: keep tus.** Suite uses tus (`apps/api/src/repo/uploadRepo.ts`
shape implies tus); rewrite preserves. Customers use `tus-js-client`
or any tus-compatible client.

### Q7. Customer webhook signing

**DECIDED: HMAC-SHA-256 with operator pepper.** Suite shell uses the
same pattern (`webhookSigner.ts`). Outbound POST carries
`X-Livepeer-Webhook-Signature: sha256=<hex>`. Customers verify via the
pepper they get on first webhook config save (one-time-reveal, same
as API keys).

### Q8. Recording feature — preserve?

**DECIDED: keep, opt-in.** Suite's `recordingBridge.ts` (file rename
to `recording-egress.ts` per "no bridge" lock) writes a parallel
HLS-VOD output during live streaming. Customer toggles per-stream;
default off in v0.1. File-write quotas tracked separately.

### Q9. Live-stream stuck-session sweep

**DECIDED: keep, configurable.** Suite's `staleStreamSweeper.ts` runs
every N seconds; cancels live streams with no recent RTMP packets.
`VIDEO_STALE_STREAM_SWEEP_INTERVAL_SECONDS` env tunes; default 60s.

### Q10. apps/playback-origin Dockerfile and stub package — discard

**DECIDED: discard.** §5.2 maps it to `dropped`.

### OQ1. LL-HLS rewrite layer in gateway

**DECIDED: strictly proxy the broker.** No gateway-side cache-control
/ CORS / playlist-rewrite logic. Broker's LL-HLS handler (plan
0011-followup §6.3) is the canonical source. CDN concerns (caching,
edge replication, geographic routing) are the operator's call —
they put CloudFront / Fastly / Cloudflare in front of the gateway as
needed. Gateway doesn't reinvent CDN. §3 + §8.4 narrative reflects.

### OQ2. VOD asset deletion semantics

**DECIDED: soft-delete in v0.1, hard-delete via separate ops job.**
Customer `DELETE /v1/videos/assets/{id}` flips a `deleted_at`
timestamp on `media.assets`; the row stays for retention period.
Hard-delete (broker scratch + S3 + DB row removal) runs as a
separate retention-driven janitor job per operator policy, forwarded
to a future plan. v0.1 ships only the soft-delete path. §7.2 column
note + §8.2 endpoint rows + §12 runbook item 6 + §15 forwarding
address reflect.

### OQ3. ABR ladder selection policy

**DECIDED: customer-tier.** The customer's billing tier (free /
prepaid / enterprise) determines the ABR ladder served at
session-open. Suite shape — revenue-tier-driven service quality is
standard SaaS. `VIDEO_GATEWAY_ABR_POLICY=customer-tier` is the
default; operators wanting flat-rate (all customers same ladder
regardless of tier) can flip to `operator-flat` via env config in a
future minor. v0.1 ships `customer-tier` only. §10 env var + §15
forwarding address reflect.

### OQ4. Live-stream → VOD handoff at session-end

**DECIDED: opt-in flag per stream, default off.** Customer
explicitly requests the handoff via session-open params
(`record_to_vod: true`). Default-off prevents surprise VOD storage
costs for customers who only want live streaming. Suite stops at
egress (no VOD); rewrite extends to optional auto-create. The
handoff orchestration: at session-end, if the flag was set, the
gateway creates a VOD asset row pointing at the recording-egress
output; the customer can fetch / delete it via the same VOD API
surface as direct-uploaded VOD assets. Per-stream flag, **not** a
deployment env var (see §10 note).

## 15. Out of scope (forwarding addresses)

- **`http-binary-stream@v0`** — same as 0013-openai §15; spec gap.
- **`video-worker-node` Go process** — replaced by
  `capability-broker/` + workload binaries (plan 0013-runners).
- **Customer-portal shell internals** — plan 0013-shell.
- **Wire-protocol middleware for `rtmp-ingress-hls-egress@v0`** —
  plan 0008-followup.
- **Broker-side RTMP/FFmpeg/HLS pipeline** — plan 0011-followup.
- **Chain integration** — plan 0016.
- **DRM / token-gated playback** — operator concern, not gateway
  architecture.
- **WebRTC egress / DASH egress** — out of scope for v0.1 per plan
  0011-followup §15; HLS-only.
- **Multi-region playback** — operator CDN, not gateway.
- **`livepeer-byoc/gateway-proxy/`** — not migrated (was for
  go-livepeer).
- **`livepeer-byoc/video-generation/`** — not migrated per user lock.
- **`live-transcode-runner/`** — not migrated per plan 0011-followup
  (capability-broker's mode driver replaces it).
- **VOD hard-delete janitor job** — separate future plan; v0.1 is
  soft-delete only (§14 OQ2).
- **Operator-flat-rate ABR policy** — future minor expansion of
  `VIDEO_GATEWAY_ABR_POLICY` (§14 OQ3).

---

## Appendix A — file paths cited

This monorepo:

- `docs/exec-plans/active/0013-shell-customer-portal-extraction.md`.
- `docs/exec-plans/active/0013-openai-gateway-collapse.md` — sibling.
- `docs/exec-plans/active/0013-vtuber-suite-migration.md` — sibling.
- `docs/exec-plans/completed/0011-rtmp-ingress-hls-egress-driver.md` — broker mode.
- `docs/exec-plans/completed/0011-followup-rtmp-media-pipeline.md` —
  broker media pipeline; §4.2 stream-key validation; §6.3 HLS server;
  §11.4 hardware GPU smoke.
- `docs/exec-plans/active/0008-followup-gateway-adapters-non-http-modes.md`.
- `docs/exec-plans/active/0016-chain-integrated-payment-design.md`.
- `livepeer-network-protocol/headers/livepeer-headers.md` — header set.
- `livepeer-network-protocol/modes/rtmp-ingress-hls-egress.md` — wire spec.

Suite paths cited:

- `livepeer-network-suite/livepeer-video-gateway/apps/api/src/{main,config,providers,repo,runtime,service}/...`
- `livepeer-network-suite/livepeer-video-gateway/apps/api/drizzle/{0000_initial_schema,0001_encoding_job_routes,0002_assets_selected_offering,0003_live_stream_pattern_b_fields}.sql`
- `livepeer-network-suite/livepeer-video-gateway/web-ui/src/{components,services,main.ts,index.html}` + Vite config
- `livepeer-network-suite/livepeer-video-gateway/apps/playback-origin/{package.json,README.md}` (dropped)
- `livepeer-network-suite/livepeer-video-gateway/packages/shared/src/...` (folded into types)
- `livepeer-network-suite/livepeer-video-core/src/{index,dispatch,service,types,interfaces,repo,config,adapters,dashboard,testing,providers}/...`
- `livepeer-network-suite/livepeer-video-core/examples/{minimal-shell,wallets}/` (dropped)
