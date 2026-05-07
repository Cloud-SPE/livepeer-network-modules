---
plan: 0013-vtuber
title: vtuber-gateway + vtuber-pipeline + vtuber-runner — design
status: completed
phase: shipped
opened: 2026-05-07
closed: 2026-05-07
owner: harness
related:
  - "active plan 0013-shell — customer-portal/ shared SaaS shell library (foundation)"
  - "active plan 0013-openai — openai-gateway/ collapse (sibling consumer)"
  - "completed plan 0012 — session-control-plus-media driver (broker mode)"
  - "active plan 0012-followup — session-control-plus-media media plane"
  - "active plan 0008-followup — gateway-adapters non-HTTP modes (parallel; vtuber-gateway consumes session-control mode middleware)"
  - "active plan 0016 — chain-integrated payment-daemon (chain gate for vtuber-gateway only)"
  - "user-memory project_byoc_vtuber.md — active design work on the vtuber product"
  - "user-memory reference_open_llm_vtuber_rendering.md — renderer is net-new (not from upstream)"
audience: vtuber-product maintainers planning the suite + vtuber-project absorption
---

# Plan 0013-vtuber — vtuber-gateway + pipeline + runner (design)

> **Paper-only design brief.** No code, no `package.json`, no
> `pyproject.toml`, no `pnpm-workspace.yaml` edits ship from this
> commit. Locks recorded in §14 as `DECIDED:` blocks (Q1-Q10 +
> OQ1-OQ5).

## 1. Status and scope

Scope: **the full vtuber product family** absorbs into the rewrite as
three new components:

1. `vtuber-gateway/` — customer-facing TypeScript HTTP+WS gateway;
   absorbs `livepeer-network-suite/livepeer-vtuber-gateway/`
   (the suite-side "vtuber gateway", historically called the
   "vtuber-livepeer-bridge" in suite docs — that name is retired).
2. `vtuber-pipeline/` — Python SaaS pipeline; absorbs
   `livepeer-vtuber-project/pipeline-app/` (the canonical top-level
   `livepeer-vtuber-project/`, **not** the suite's mirror submodule).
3. `vtuber-runner/` — Python session-runtime workload binary +
   browser-side avatar renderer; absorbs
   `livepeer-vtuber-project/{session-runner,avatar-renderer}/`.

Chain-gating is **mixed**: `vtuber-gateway/` emits payments, so its
production cutover gates on plan 0016 (chain-integrated payment-
daemon) reaching v1.0.0. `vtuber-pipeline/` and `vtuber-runner/` are
pre-1.0.0-shippable — the pipeline doesn't sign tickets (it consumes
the gateway's API), and the runner is workload-side (consumes broker
dispatch; receiver-side `payment-daemon/` is already chain-stubbed
for receiver-mode).

Out of scope:

- The orchestrator-side `vtuber-worker-node` Go binary — replaced by
  `capability-broker/` + `vtuber-runner/` (workload). See
  `docs/design-docs/migration-from-suite.md` §2 row "vtuber-worker-node".
- Spec-level mode definitions — `session-control-plus-media@v0` is
  frozen at the shape plan 0012 + plan 0012-followup deliver.
- Customer-portal shell internals — plan 0013-shell.
- Wire-protocol middleware — `gateway-adapters/` (plans 0008 +
  0008-followup).

The three components ship in the same monorepo, **MIT-licensed**,
under one tech-stack lock with explicit Python and browser-TS variances
(per §6).

## 2. What predecessor work left unfinished

Plan 0012 ships `session-control-plus-media@v0` mode driver in
`capability-broker/` (session-open phase). Plan 0012-followup
extends the broker side with the media-plane (transcoder/control-WS
fan-out). Both are parallel implementation efforts; neither ships the
gateway side, the pipeline, or the runner.

The suite ships `livepeer-vtuber-gateway/` as a Cloud-SPE shell of
the openai engine forked for vtuber semantics
(`livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/http/vtuber/sessions.ts:36-80`
mirrors openai's auth+billing pattern but adds session-open routes
+ a worker control-WS relay). It carries the same two-package
co-evolution debt as the openai shell — the rewrite collapse cleans
it up here.

The vtuber-project (canonical top-level) ships:

- `pipeline-app/` (Python, FastAPI) — multi-binary saas pipeline
  (mock-youtube chat source, egress workers, streams orchestrator).
- `session-runner/` (Python, FastAPI) — workload runtime running on
  orch hosts: drives the avatar renderer (headless Chromium),
  publishes audio-mux to a trickle subscriber, exposes a control-WS.
- `avatar-renderer/` (browser TS, three.js + @pixiv/three-vrm +
  WebCodecs) — VRM scene rendered in headless Chromium, encodes H.264
  frames over WS to the session-runner.

The vtuber-project is preserved but its **canonical home** moves into
the rewrite monorepo at v1.0.0. That migration is what this brief
describes.

## 3. Reference architecture

```
  pipeline-app's customers              direct B2B integrators
        │ HTTPS                              │  HTTPS
        ▼                                    │
  ┌──────────────────────────┐                │
  │  vtuber-pipeline/  (Py)  │                │
  │   pipeline-streams /     │                │
  │   pipeline-egress /      │                │
  │   pipeline-mock-youtube  │                │
  │   - holds ONE shared     │                │
  │     LIVEPEER_VTUBER_     │                │
  │     GATEWAY_API_KEY      │                │
  │     (meta-customer)      │                │
  │   - bills its own        │                │
  │     customers internally │                │
  └──────────────────────────┘                │
                │ HTTPS (shared key)          │ HTTPS (per-customer key)
                ▼                             ▼
  ┌──────────────────────────────────────────────────────────┐
  │  vtuber-gateway/  (Node 20 + TS + Fastify 5)             │
  │   - SaaS surfaces: customer auth + billing + Stripe +    │
  │     admin (delegates to customer-portal/)                │
  │   - sees ONE high-volume customer = pipeline-app, plus   │
  │     direct integrators (per-customer keys via portal SPA)│
  │   - product routes:                                      │
  │       POST   /v1/vtuber/sessions          (session-open) │
  │       GET    /v1/vtuber/sessions/:id                     │
  │       POST   /v1/vtuber/sessions/:id/end                 │
  │       POST   /v1/vtuber/sessions/:id/topup               │
  │       WS     /v1/vtuber/sessions/:id/control (relay)     │
  │   - calls payment-daemon (sender) + capability-broker    │
  │   - mints session-scoped child bearer for control-WS     │
  └──────────────────────────────────────────────────────────┘
         │  (capability-broker dispatch via Livepeer-Mode)
         ▼
  ┌──────────────────────────────────────────────────────────┐
  │  capability-broker (orch host)                           │
  │   session-control-plus-media@v0 driver                   │
  │   - SessionRunnerControl.ReportWorkUnits(stream) gRPC    │
  │     bidi-stream from runner; broker accumulates into     │
  │     atomic.Uint64 (LiveCounter); interim-debit ticker    │
  │     reads via CurrentUnits() — see plan 0012-followup §8 │
  └──────────────────────────────────────────────────────────┘
         │
         ▼
  ┌──────────────────────────────────────────────────────────┐
  │  vtuber-runner/  (Python 3.12 + FastAPI; one per session)│
  │   - drives headless Chromium → avatar-renderer/          │
  │   - encodes H.264 over WebCodecs in the browser          │
  │   - mux audio + video → trickle publish                  │
  │   - control-WS bidirectional fan-out                     │
  │   - reports per-second monotonic-delta work-units back   │
  │     to broker over the runner-IPC gRPC bidi-stream       │
  └──────────────────────────────────────────────────────────┘
                      │ WebSocket
                      ▼
                 avatar-renderer/  (browser TS in Chromium)
                 three.js + @pixiv/three-vrm + WebCodecs
                 - VRM scene
                 - idle anims (breathing / blink / sway)
                 - WS encoded-frame stream
                 - control-WS (set_expression / set_lookat / speak / clear_speaking)
```

`vtuber-gateway/` is the *gateway* (payer-side). The suite's name
"vtuber-livepeer-bridge" / "the bridge" is **retired**; the rewrite
calls it "vtuber-gateway". The suite name survives only in citation
strings.

`vtuber-pipeline/` is **product code** that sits *above* the gateway
(it consumes the gateway's sessions API). It's not infrastructure;
it's the product the customer logs into. Per OQ4 lock, pipeline-app
acts as a **meta-customer** of `vtuber-gateway` (B2B SaaS-on-SaaS):
it holds **one** shared `LIVEPEER_VTUBER_GATEWAY_API_KEY` per
deployment, bills its own customers internally, and the gateway
sees pipeline as a single high-volume customer. Pipeline-app's
customers do **not** sign up to vtuber-gateway directly.

**Direct B2B integrators** (non-pipeline customers integrating
vtuber-gateway directly) sign up via the gateway's own portal SPA
and receive **per-customer** API keys — same flow as openai-gateway
/ video-gateway. Both modes coexist.

`vtuber-runner/` is the **workload binary** running on orch hosts.
It pairs with the broker's `session-control-plus-media@v0` mode driver
(plan 0012). The avatar-renderer is the runner's child Chromium
process; both ship as one logical workload artifact (same Docker
image, two payloads). The runner reports per-second work-units to
the broker over the `SessionRunnerControl.ReportWorkUnits(stream)`
gRPC bidi-stream (control-IPC channel; plan 0012-followup §8).

## 4. Component layout

### 4.1 vtuber-gateway/

```
vtuber-gateway/
  AGENTS.md
  DESIGN.md
  README.md
  Makefile
  Dockerfile
  compose.yaml
  compose.prod.yaml
  package.json                ← @livepeer-rewrite/vtuber-gateway; ESM-only
  tsconfig.json
  vitest.config.ts
  drizzle.config.ts
  migrations/
    0000_vtuber_init.sql      ← vtuber.sessions, vtuber.session_bearers, vtuber.usage_records, vtuber.node_health
  src/
    main.ts                   ← composition root
    config.ts                 ← Zod env (vtuber-specific)
    server.ts                 ← Fastify factory
    livepeer/                 ← wire layer (consumes gateway-adapters/)
      headers.ts
      payment.ts
      session-control.ts      ← session-control-plus-media@v0 send wrapper
    routes/
      sessions.ts             ← POST /v1/vtuber/sessions  + GET /:id + POST /:id/end + /:id/topup
      session-control-ws.ts   ← WS /v1/vtuber/sessions/:id/control
      account.ts              ← shell delegator
      admin.ts                ← shell delegator + product extras
      billing-topup.ts        ← shell delegator
      stripe-webhook.ts       ← shell delegator
    pricing/
      vtuberRateCard.ts       ← per-second pricing (vtuber pricing model)
    repo/
      vtuberSessions.ts
      vtuberSessionBearers.ts
      vtuberUsageRecords.ts
      nodeHealth.ts
    service/
      sessions/
        openSession.ts
        closeSession.ts
        topupSession.ts
        relay.ts              ← live worker↔customer WS relay
      auth/
        sessionBearer.ts      ← shell-shared, but vtuber-gateway issues child bearers
        workerControlBearer.ts ← worker-control bearer minting (vtuber-specific)
      nodes/
        vtuberRegistry.ts     ← vtuber-capable node roster (resolver-derived)
        registryRefresher.ts  ← polls coordinator
        circuitBreaker.ts     ← per-node failure tracking
        scheduler.ts          ← node selection
        nodebook.ts           ← node-state cache
      billing/
        vtuberBilling.ts      ← per-second rollups against the vtuber rate-card
      providers/
        workerClient.ts       ← HTTP client for /api/sessions/start on the runner
    types/
      vtuber.ts               ← session-open request/response Zod schemas
    frontend/
      portal/
        components/
          portal-vtuber-sessions.ts  ← customer "my sessions" page
        index.html
      admin/
        components/
          admin-vtuber-sessions.ts
          admin-vtuber-rate-card.ts
        index.html
  test/
    integration/
    smoke/
```

### 4.2 vtuber-pipeline/

```
vtuber-pipeline/
  AGENTS.md
  DESIGN.md
  README.md
  Makefile
  Dockerfile                  ← multi-arch; one image per pipeline binary
  compose.yaml
  pyproject.toml              ← package name `vtuber_pipeline`
  uv.lock                     ← uv-managed
  src/
    vtuber_pipeline/
      __init__.py
      mock_youtube/           ← chat-source provider (mock; production uses real youtube)
        runtime/entrypoint.py
        config/
        types/
        repo/
        service/
        ui/
      streams/                ← streams orchestrator (calls vtuber-gateway sessions API)
        runtime/entrypoint.py
        config/
        providers/
          bridge.py           ← HTTP client to vtuber-gateway (rename from "bridge")
          youtube.py          ← youtube provider (live broadcast)
          egress_admin.py     ← egress-worker admin
        repo/
        service/
        types/
        ui/
      egress/                 ← RTMP push to youtube-live
        runtime/entrypoint.py
        config/
        providers/
          ffmpeg_runner.py    ← ffmpeg subprocess wrapper
        repo/
        service/
        types/
        ui/
        _test_fakes.py        ← test fakes
  tests/
    unit/
    integration/
```

The pipeline ships **three console scripts** declared in
`pyproject.toml` (`pipeline-mock-youtube`, `pipeline-egress`,
`pipeline-streams`) — same shape as
`livepeer-vtuber-project/pipeline-app/pyproject.toml:[project.scripts]`.

### 4.3 vtuber-runner/

```
vtuber-runner/
  AGENTS.md
  DESIGN.md
  README.md
  Makefile
  Dockerfile                  ← three-stage: (1) Vite renderer build, (2) Python deps via uv, (3) runtime with Playwright + chromium-headless-shell + ffmpeg + renderer dist
  compose.yaml                ← dev compose (single runner + a fake gateway)
  pyproject.toml              ← package name `session_runner` (Python)
  uv.lock
  src/
    session_runner/
      __init__.py
      __main__.py             ← `python -m session_runner` entry
      runtime/
        entrypoint.py
        app.py                ← FastAPI factory
        session_factory.py
      config/
        settings.py
      types/
        api.py
        media.py
        state.py
      ui/
        http.py               ← /api/sessions/* routes
      service/
        manager.py            ← top-level session manager
        session_pipeline.py   ← per-session pipeline assembly
        pipeline.py           ← conversation-loop façade
        conversation.py       ← OLV conversation glue (vendored upstream)
        emotion_mapper.py     ← LLM-output → renderer expression code
        renderer.py           ← renderer-driver façade
        renderer_chromium.py  ← headless Chromium driver
        renderer_factory.py
        renderer_fixture.py   ← test renderer
        control_ws.py         ← worker control-WS endpoint
        control_dispatcher.py
        channels.py           ← inter-task pub/sub
        output_sink.py
        audio_mux_sink.py
        mux_pipeline.py
        egress_segment_sink.py
        trickle_sink.py
      providers/
        llm_livepeer.py       ← LLM via openai-gateway/livepeer
        llm_mock.py
        tts_livepeer.py       ← TTS via openai-gateway/livepeer
        tts_mock.py
        trickle.py            ← trickle publisher client
        egress_publisher.py
        olv_loader.py         ← OLV upstream loader (third_party/)
        telemetry.py
        vector_log.py
    avatar_renderer/           ← TS sub-workspace (browser code)
      package.json             ← @livepeer-rewrite/avatar-renderer
      tsconfig.json
      vite.config.ts
      vitest.config.ts
      index.html
      src/
        main.ts                ← three.js + VRM + WebCodecs encoder + WS
        scene/
        controllers/
        encoders/
      tests/
  third_party/
    olv/                       ← vendored OLV upstream (canonical place; vendor lift, not submodule)
      UPSTREAM.md              ← upstream commit hash + rebase procedure
  tests/
    unit/
    integration/
```

The avatar-renderer is a **sub-workspace** of `vtuber-runner/`,
sharing the runner's Docker image build but with its own
`package.json` and Vite config. Per OQ1 lock, the renderer is
**always rebuilt from source** in stage 1 of the runner's three-stage
Dockerfile (Vite renderer build → Python deps via uv → runtime with
Playwright + chromium-headless-shell). No pre-built bundle artifact
is published — one source of truth, no version-drift between a
published artifact and the runner image. Bundle size is small enough
the rebuild cost is negligible. Stage 1 emits `avatar_renderer/dist/`,
stage 3 copies it into the runtime image.

Per OQ2 lock, OLV (Open-LLM-VTuber) upstream is **vendored** at
`third_party/olv/` (not a git submodule). Submodule complexity
(init/update, recursive clone, CI gotchas) isn't worth it for OLV's
slow upstream release cadence. A `third_party/olv/UPSTREAM.md`
documents the upstream commit hash + rebase procedure for pulling
new versions. Matches user-memory `feedback_submodule_url_protocol.md`
(HTTPS-only) preference + the rewrite's clean-slate philosophy +
the suite's existing vendored layout.

## 5. Source-to-destination file map

### 5.1 vtuber-gateway/

| Source | Target |
|---|---|
| `livepeer-network-suite/livepeer-vtuber-gateway/src/main.ts` | `vtuber-gateway/src/main.ts` |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/http/vtuber/sessions.ts:1-80` (and remainder; ≈350 LOC) | `vtuber-gateway/src/routes/sessions.ts` |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/http/vtuber/relay.ts:1-60` (and remainder; ≈200 LOC) | `vtuber-gateway/src/routes/session-control-ws.ts` + `vtuber-gateway/src/service/sessions/relay.ts` |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/http/admin/routes.ts` | `vtuber-gateway/src/routes/admin.ts` (delegator + vtuber extras) |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/http/billing/topup.ts` | `vtuber-gateway/src/routes/billing-topup.ts` (delegator) |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/http/stripe/webhook.ts` | `vtuber-gateway/src/routes/stripe-webhook.ts` (delegator) |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/http/middleware/{auth,rateLimit}.ts` | `customer-portal/src/middleware/{authPreHandler,rateLimitPreHandler}.ts` (already covered by 0013-shell §5.4) |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/http/errors.ts` | `customer-portal/src/middleware/errors.ts` (already covered) |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/service/auth/sessionBearer.ts` | `customer-portal/src/auth/sessionBearer.ts` (covered by 0013-shell §5.1) |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/service/auth/workerControlBearer.ts` | `vtuber-gateway/src/service/auth/workerControlBearer.ts` (vtuber-specific; not in shell) |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/service/payments/vtuberSession.ts` | `vtuber-gateway/src/service/sessions/openSession.ts` (per-session payment-daemon flow) |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/service/payments/createPayment.ts` | folded into `vtuber-gateway/src/routes/sessions.ts` |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/service/billing/vtuberBilling.ts` | `vtuber-gateway/src/service/billing/vtuberBilling.ts` |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/service/nodes/{vtuberRegistry,registryRefresher,scheduler,nodebook,circuitBreaker,loader,staticWorkersLoader}.ts` | `vtuber-gateway/src/service/nodes/*.ts` |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/service/nodes/quoteRefresher.ts` | **deleted** (quote-free flow per `0013-openai` §3.4) |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/providers/workerClient.ts` | `vtuber-gateway/src/service/providers/workerClient.ts` |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/providers/{nodeClient,payerDaemon,serviceRegistry}.ts` | replaced by direct `gateway-adapters/` + `payment-daemon/` gRPC. `serviceRegistry` deleted (quote-free). |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/repo/{vtuberSessions,vtuberSessionBearers,nodeHealth}.ts` | `vtuber-gateway/src/repo/*.ts` |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/repo/{usageRecords,usageRollups}.ts` | `vtuber-gateway/src/repo/vtuberUsageRecords.ts` |
| `livepeer-network-suite/livepeer-vtuber-gateway/src/types/vtuber.ts` | `vtuber-gateway/src/types/vtuber.ts` |
| `livepeer-network-suite/livepeer-vtuber-gateway/migrations/0007_vtuber_sessions.sql` | `vtuber-gateway/migrations/0000_vtuber_init.sql` (renumbered + namespaced to `vtuber.*`) |
| `livepeer-network-suite/livepeer-vtuber-gateway/migrations/0008_vtuber_session_bearers.sql` | `vtuber-gateway/migrations/0000_vtuber_init.sql` (folded) |
| `livepeer-network-suite/livepeer-vtuber-gateway/migrations/0009_vtuber_session_payer_work_id.sql` | `vtuber-gateway/migrations/0000_vtuber_init.sql` (folded) |
| `livepeer-network-suite/livepeer-vtuber-gateway/migrations/0000…0006_*` (shell-shared schema) | covered by `customer-portal/migrations/0000_app_init.sql` (0013-shell §5.5) |
| `livepeer-network-suite/livepeer-vtuber-gateway/Dockerfile` + `compose.yaml` + `compose.prod.yaml` | `vtuber-gateway/{Dockerfile,compose.yaml,compose.prod.yaml}` |

### 5.2 vtuber-pipeline/

Source: `/home/mazup/git-repos/livepeer-cloud-spe/livepeer-vtuber-project/pipeline-app/`
(canonical, top-level repo per user-memory locks; **not** the suite
mirror).

| Source | Target |
|---|---|
| `livepeer-vtuber-project/pipeline-app/pyproject.toml` | `vtuber-pipeline/pyproject.toml` (rename project from `pipeline` to `vtuber-pipeline` for namespace clarity in monorepo) |
| `livepeer-vtuber-project/pipeline-app/Dockerfile` | `vtuber-pipeline/Dockerfile` |
| `livepeer-vtuber-project/pipeline-app/src/pipeline/__init__.py` | `vtuber-pipeline/src/vtuber_pipeline/__init__.py` |
| `livepeer-vtuber-project/pipeline-app/src/pipeline/mock_youtube/**` | `vtuber-pipeline/src/vtuber_pipeline/mock_youtube/**` |
| `livepeer-vtuber-project/pipeline-app/src/pipeline/streams/**` | `vtuber-pipeline/src/vtuber_pipeline/streams/**` |
| `livepeer-vtuber-project/pipeline-app/src/pipeline/streams/providers/bridge.py` | `vtuber-pipeline/src/vtuber_pipeline/streams/providers/gateway.py` (renamed from `bridge.py` per "no bridge" memory lock) |
| `livepeer-vtuber-project/pipeline-app/src/pipeline/streams/providers/youtube.py` | `vtuber-pipeline/src/vtuber_pipeline/streams/providers/youtube.py` |
| `livepeer-vtuber-project/pipeline-app/src/pipeline/streams/providers/egress_admin.py` | `vtuber-pipeline/src/vtuber_pipeline/streams/providers/egress_admin.py` |
| `livepeer-vtuber-project/pipeline-app/src/pipeline/egress/**` | `vtuber-pipeline/src/vtuber_pipeline/egress/**` |
| `livepeer-vtuber-project/pipeline-app/tests/**` | `vtuber-pipeline/tests/**` |

### 5.3 vtuber-runner/

| Source | Target |
|---|---|
| `livepeer-vtuber-project/session-runner/pyproject.toml` | `vtuber-runner/pyproject.toml` |
| `livepeer-vtuber-project/session-runner/Dockerfile` | `vtuber-runner/Dockerfile` |
| `livepeer-vtuber-project/session-runner/src/session_runner/**` | `vtuber-runner/src/session_runner/**` (~30 .py files; 2-level deep service tree per §4.3) |
| `livepeer-vtuber-project/session-runner/third_party/open-llm-vtuber/` | `vtuber-runner/third_party/olv/` (vendor lift per OQ2; not a git submodule) |
| (new) | `vtuber-runner/third_party/olv/UPSTREAM.md` (new file: upstream commit hash + rebase procedure) |
| `livepeer-vtuber-project/session-runner/tests/**` | `vtuber-runner/tests/**` |
| `livepeer-vtuber-project/avatar-renderer/package.json` | `vtuber-runner/src/avatar_renderer/package.json` |
| `livepeer-vtuber-project/avatar-renderer/tsconfig.json` + `vite.config.ts` + `vitest.config.ts` | `vtuber-runner/src/avatar_renderer/{tsconfig.json,vite.config.ts,vitest.config.ts}` |
| `livepeer-vtuber-project/avatar-renderer/index.html` | `vtuber-runner/src/avatar_renderer/index.html` |
| `livepeer-vtuber-project/avatar-renderer/src/main.ts` | `vtuber-runner/src/avatar_renderer/src/main.ts` |
| `livepeer-vtuber-project/avatar-renderer/tests/**` | `vtuber-runner/src/avatar_renderer/tests/**` |
| `livepeer-vtuber-project/Grifter_Squaddie__1419.vrm` (test VRM) | `vtuber-runner/tests/fixtures/Grifter_Squaddie__1419.vrm` |
| `livepeer-vtuber-project/avatar-renderer/dist/` (pre-bundled) | regenerated by Vite during runner Docker build; not committed |
| `livepeer-vtuber-project/{biome.json,FRONTEND.md,DESIGN.md,PRODUCT_SENSE.md,PLANS.md}` | folded into `vtuber-runner/AGENTS.md` + `DESIGN.md` |
| `livepeer-vtuber-project/infrastructure/` | folded into `vtuber-runner/Makefile` + `compose.yaml` |
| `livepeer-vtuber-project/scripts/` | folded into `vtuber-runner/scripts/` (kept as-is) |

The vtuber-project's top-level `pnpm-workspace.yaml` and `pyproject.toml`
**do not migrate as files** — the rewrite root's pnpm + uv configs
absorb the vtuber sub-workspaces directly.

## 6. Tech-stack lock + variance justification

### 6.1 Canonical lock (vtuber-gateway/)

Same as `customer-portal/` and `openai-gateway/`: Fastify 5 + Zod 3 +
drizzle-orm + ESM TS 5 + Node 20+ + pnpm + Postgres 16 + Redis 7 +
Stripe 14 + Lit 3 + RxJS 7. No variance.

### 6.2 Variance: vtuber-pipeline/ — Python 3.12 + FastAPI

Justification: the suite's `livepeer-vtuber-gateway/` is TypeScript,
but the **vtuber-project pipeline** is Python and has been since
inception (`livepeer-vtuber-project/pipeline-app/pyproject.toml`).
The pipeline integrates with Python-only providers (FastAPI for the
HTTP surface + httpx + websockets + structlog). A TS port is a
multi-quarter rewrite with zero customer-visible improvement; the
pipeline stays Python.

Pinned versions (matching vtuber-project today):
- Python ≥3.12 (matches `livepeer-vtuber-project/pyproject.toml:requires-python`)
- FastAPI ≥0.115,<1.0
- uvicorn[standard] ≥0.32
- Pydantic ≥2.9
- structlog ≥24.4
- httpx ≥0.27
- websockets ≥13
- Build via uv (matches vtuber-project root) — `uv` workspace member
  of the rewrite repo's root uv config.

### 6.3 Variance: vtuber-runner/ — Python 3.12 + FastAPI + Chromium

Justification: same as 6.2 — the runner is Python because
session-runner orchestrates Chromium (via Playwright /
asyncio.subprocess), trickle audio publishing (av + numpy +
aiohttp), and msgpack control. A TS port loses async + numpy
support patterns the runner depends on.

Pinned versions (matching session-runner today):
- Python ≥3.12
- FastAPI ≥0.115
- av ≥14.0
- numpy ≥2.0
- aiohttp ≥3.10
- msgpack ≥1.1
- Playwright ≥1.48 (probe / dev only)
- structlog ≥24.4
- pydantic ≥2.9

OLV (Open-LLM-VTuber) is **vendored** at `vtuber-runner/third_party/olv/`
per OQ2 lock — vendor lift, not a git submodule. `UPSTREAM.md` in
that directory documents the upstream commit hash + rebase procedure.

### 6.4 Variance: avatar-renderer (browser TS)

Justification: the renderer **is** a browser app — three.js +
@pixiv/three-vrm + WebCodecs run in headless Chromium. There is no
node-native equivalent; this is browser-only by definition. Pinned
versions (matching vtuber-project today):

- TS 5+
- three ^0.184
- @pixiv/three-vrm ^3.5
- @msgpack/msgpack ^3.1
- Vite 8 + Vitest 2

Vite is the bundler (matches vtuber-project); does not affect the
rest of the monorepo's pnpm workspace.

The renderer is `private: true` in package.json; never published.
Per OQ1 lock, the renderer is **rebuilt from source** in stage 1 of
the runner's three-stage Dockerfile (Vite renderer → Python deps via
uv → runtime). No pre-built bundle artifact is published — one
source of truth, no version-drift between artifact and runner image.

### 6.5 Variance: openai/customer-portal share `app.*` schema; vtuber owns `vtuber.*`

Per plan 0013-shell Q6 lock + plan 0013-openai Q1 exception, the
vtuber-gateway lands its product schema in a fresh `vtuber.*` Postgres
namespace. No leak across products.

## 7. DB schema

### 7.1 vtuber-gateway/migrations/

| Table | Purpose | Source |
|---|---|---|
| `vtuber.sessions` | One row per opened session: customer_id, status (`starting/active/ending/ended/errored`), node_id, node_url, worker_session_id, control_url, expires_at, params_json, error_code. | suite `migrations/0007_vtuber_sessions.sql:7-24` |
| `vtuber.session_bearers` | Per-session child bearer hashes; revoked_at column. | suite `migrations/0008_vtuber_session_bearers.sql` |
| `vtuber.usage_records` | Per-session usage rollups (seconds; cents). | suite `livepeer-vtuber-gateway/src/repo/usageRecords.ts` |
| `vtuber.node_health` | Per-node liveness + last-success timestamps for circuit-breaker. | suite `livepeer-vtuber-gateway/src/repo/nodeHealth.ts` |
| `vtuber.session_payer_work_id` | Mapping `session_id ↔ payer_work_id` for ledger reconciliation. | suite `migrations/0009_vtuber_session_payer_work_id.sql` |
| `vtuber.rate_card_session` | Per-second pricing tier table. | suite (implicit; rewrite makes it explicit) |

Folded into a single `0000_vtuber_init.sql` since they all land at
once in phase 1 of the migration. drizzle-kit emits the SQL.

### 7.2 vtuber-pipeline/

The pipeline is Python; it doesn't run drizzle. Pipeline persistence is
limited to Pydantic models + per-binary state (no shared DB). Egress
worker writes ffmpeg progress to a local file; streams orchestrator
holds session state in memory + recovers from the gateway's API. No
schema migrations.

### 7.3 vtuber-runner/

Per-process state only; no Postgres. Session manager holds in-memory
session records keyed on `session_id`; Chromium child manages its own
state. No migrations.

## 8. Customer-facing surfaces

### 8.1 vtuber-gateway routes

Two API-key flows coexist per OQ4 lock: (a) **shared-per-deployment
default** — pipeline-app holds one `LIVEPEER_VTUBER_GATEWAY_API_KEY`
and acts as a meta-customer (single high-volume customer; pipeline-app
billing is internal to pipeline-app); (b) **per-customer opt-in** —
direct B2B integrators sign up via the gateway's portal SPA and
receive per-customer keys (same flow as openai-gateway / video-gateway).
Both routes serve both modes; the auth middleware doesn't distinguish.

| Method + path | Mode | Capability | Notes |
|---|---|---|---|
| `POST /v1/vtuber/sessions` | `session-control-plus-media@v0` | `livepeer:vtuber-session` | Session-open. Body: persona, vrm_url, llm_provider, tts_provider, target_youtube_broadcast. Returns `{session_id, control_url, expires_at, session_child_bearer}`. Per-second metering kicks in on first frame. |
| `GET /v1/vtuber/sessions/:id` | (REST) | n/a | Customer or session-bearer auth. Returns status. |
| `POST /v1/vtuber/sessions/:id/end` | (REST) | n/a | Customer kill switch. |
| `POST /v1/vtuber/sessions/:id/topup` | (REST) | n/a | Add USD-cents balance to a running session (avoid forced kill on insufficient balance). |
| `WS /v1/vtuber/sessions/:id/control` | `session-control-plus-media@v0` (control plane) | `livepeer:vtuber-session` | Bidirectional WS; child-bearer auth. Worker sends events (`speaking_started`, `speaking_ended`, `expression_changed`, `frame_rate_drop`); customer sends commands (`set_expression`, `set_lookat`, `speak`, `clear_speaking`). |

### 8.2 vtuber-pipeline routes (customer-product side, NOT the gateway)

The customer-facing API surface lives at the **pipeline**; pipeline
calls `vtuber-gateway` as the **meta-customer** using the shared
`LIVEPEER_VTUBER_GATEWAY_API_KEY` (per OQ4 lock). Pipeline-app's
own customers never see vtuber-gateway directly; pipeline-app
handles its own per-customer billing internally.

`pipeline-mock-youtube` — chat source (mock; replaced by real YouTube
provider in production).
- `POST /chat/messages` — emit a chat message into the live pool.
- `WS /chat/stream` — SSE-style stream of chat to subscribers.

`pipeline-streams` — streams orchestrator.
- `POST /streams` — start a youtube broadcast + open a vtuber session
  via the gateway (using the shared meta-customer API key).
- `GET /streams` — list customer's streams (pipeline-app's customer,
  not vtuber-gateway's).
- `POST /streams/:id/end` — terminate.

`pipeline-egress` — RTMP push worker.
- `POST /egress` — register an outbound RTMP push.
- `GET /egress/:id/health` — ffmpeg subprocess health.

### 8.3 vtuber-runner routes (workload side)

`session-runner` (Python FastAPI):
- `POST /api/sessions/start` — broker-initiated session-open. Body
  carries the worker-control bearer, persona, vrm_url, etc.
- `POST /api/sessions/:id/stop` — broker-initiated termination.
- `GET /api/sessions/:id/status` — broker poll.
- `WS /api/sessions/:id/control` — control-WS upstream of the gateway
  relay.
- `GET /options` — broker scrape (returns offerings; standardized per
  plan 0018).
- `GET /healthz`.

The avatar-renderer is loaded by Chromium from
`http://localhost:<port>/?vrm=<url>&ws=<ws_url>&width=...` — see
`livepeer-vtuber-project/avatar-renderer/src/main.ts:1-24` for
parameter docs.

## 9. Cross-component dependencies

### 9.1 vtuber-gateway

```
vtuber-gateway/
  package.json:
    dependencies:
      "@livepeer-rewrite/customer-portal":  "workspace:*"
      "@livepeer-rewrite/gateway-adapters": "workspace:*"
      fastify, zod, drizzle-orm, pg, ioredis, stripe, lit, rxjs, ws
```

Imports same shell + adapters surface as `openai-gateway/`. Adds:
- `gateway-adapters/modes/session-control-plus-media` (when 0008-followup
  ships that subpath; until then, vtuber-gateway carries an internal
  send-shim).
- `ws` for native WebSocket relay (Fastify's `@fastify/websocket` is
  the integration; pinned matching suite).

### 9.2 vtuber-pipeline

Pure Python; not in the same pnpm workspace; lives as a uv workspace
member.

- **Imports `customer-portal/`** for its own SaaS shell needs —
  signup/login/billing/portal-SPA primitives for **pipeline-app's**
  customers (pipeline-app is itself a SaaS product). Per plan
  0013-shell's per-product separate-businesses framing.
- **Dials `vtuber-gateway/` over HTTPS** using
  `LIVEPEER_VTUBER_GATEWAY_API_KEY` (shared-per-deployment, single
  meta-customer key — OQ4 lock). The HTTP client lives at
  `vtuber-pipeline/src/vtuber_pipeline/streams/providers/gateway.py`
  (formerly `bridge.py`).
- **Does not** issue or manage per-pipeline-customer API keys on
  vtuber-gateway. Pipeline-app's per-customer billing is its own
  concern; vtuber-gateway sees pipeline as a single high-volume
  customer.

### 9.3 vtuber-runner

Pure Python + browser TS; depends on:
- `livepeer-network-protocol/proto/livepeer/payments/v1/...` for the
  receiver-side ticket validation surface (consumed via the broker, not
  directly).
- `capability-broker/` (workload-side; over HTTP — `/api/sessions/start`
  is broker → runner).
- The avatar-renderer is a sub-build artifact bundled into the runner's
  Docker image at build time.

The runner does **not** import `customer-portal/` or
`gateway-adapters/`. Runners are workload binaries that consume
broker dispatch, not gateway peers.

## 10. Configuration surface

### 10.1 vtuber-gateway env

In addition to `customer-portal/` + `gateway-adapters/` env vars (see
plans 0013-shell §10 + 0013-openai §10):

| Env var | Required | Purpose |
|---|---|---|
| `LIVEPEER_BROKER_URL` | yes | Same as openai-gateway. |
| `LIVEPEER_PAYER_DAEMON_SOCKET` | yes | Same. |
| `LIVEPEER_PAYER_DEFAULT_FACE_VALUE_WEI` | yes | Default `face_value` per session-open. |
| `VTUBER_DEFAULT_OFFERING` | no | Default `Livepeer-Offering` if request omits. |
| `VTUBER_SESSION_DEFAULT_TTL_SECONDS` | no (default `3600`) | Session expires_at = now + this. |
| `VTUBER_RATE_CARD_USD_PER_SECOND` | no (default seeded by migration) | Default per-second rate; overridable per offering. |
| `VTUBER_WORKER_CALL_TIMEOUT_MS` | no (default `15000`) | Per-runner HTTP timeout. |
| `VTUBER_RELAY_MAX_PER_SESSION` | no (default `8`) | Max customer WS connections per session_id. |
| `VTUBER_SESSION_BEARER_TTL_SECONDS` | no (default `7200`) | Child-bearer TTL; ≥ session TTL by 2× safety. |

### 10.2 vtuber-pipeline env

Each binary has its own:

`pipeline-mock-youtube`:
- `MOCK_YOUTUBE_PORT`, `MOCK_YOUTUBE_LOG_LEVEL`.

`pipeline-streams`:
- `LIVEPEER_VTUBER_GATEWAY_URL` — HTTPS URL of vtuber-gateway.
- `LIVEPEER_VTUBER_GATEWAY_API_KEY` — **single shared meta-customer
  key per deployment** (OQ4 lock). Pipeline-app holds one key on
  vtuber-gateway; pipeline-app's customers never see this key.
- `YOUTUBE_API_*`, `EGRESS_ADMIN_URL`.

`pipeline-egress`:
- `EGRESS_PORT`, `FFMPEG_BINARY`, `MAX_CONCURRENT_STREAMS`.

### 10.3 vtuber-runner env

| Env var | Required | Purpose |
|---|---|---|
| `RUNNER_PORT` | no (default `8080`) | FastAPI bind. |
| `RENDERER_DIST_DIR` | no | Path to bundled avatar-renderer dist; defaults to in-image path. |
| `CHROMIUM_PATH` | no | Override for the Chromium binary. |
| `LIVEPEER_OPENAI_GATEWAY_URL` | yes | LLM/TTS endpoint (the runner consumes openai-gateway). |
| `LIVEPEER_OPENAI_GATEWAY_API_KEY` | yes | API key minted by an operator-test customer. |
| `OLV_MODEL_DIR` | no | Path to vendored OLV model assets. |
| `TRICKLE_PUBLISH_URL` | yes | Target trickle subscriber (broker-provided). |
| `WORKER_CONTROL_BEARER_PUBKEY` | yes | Public key the gateway signs control-WS bearers with. |

YAML config (vtuber-runner): optional `runner.yaml` under
`/etc/vtuber-runner/` for offering definitions consumed by `GET /options`.
Same shape as `host-config.yaml` in plan 0018 (broker-side roster).

## 11. Conformance / smoke tests

### 11.1 vtuber-gateway smokes

- `session-open.smoke.ts` — POST `/v1/vtuber/sessions`. Asserts: 202;
  `vtuber.sessions` row created; payer-daemon called with
  `(face_value, recipient, "livepeer:vtuber-session", offering)`;
  worker `POST /api/sessions/start` invoked with bearer header;
  control_url returned.
- `session-control-ws.smoke.ts` — connect to control-WS with child
  bearer; send `speak`; observe `speaking_started` event from the
  worker shim; per-second debits accrue in `vtuber.usage_records`.
- `session-end.smoke.ts` — POST `/end`; ledger flush; bearers revoked;
  control-WS closed.
- `topup-mid-session.smoke.ts` — POST `/topup` mid-session; balance
  increments; session continues past previous `expires_at`.

### 11.2 vtuber-pipeline smokes

- `streams-orchestrator.smoke.py` — pytest. Mock the gateway; `POST
  /streams` → opens a session via `gateway.py`, registers an egress
  worker with a deterministic playback URL, returns the streams row.
- `egress.smoke.py` — pytest. Start an egress worker against a fake
  RTMP origin; assert ffmpeg subprocess starts + reports progress.
- `mock-youtube.smoke.py` — pytest. Inject 5 chat messages; subscribe;
  assert all received.

### 11.3 vtuber-runner smokes

- `session-start.smoke.py` — pytest. POST `/api/sessions/start` with a
  fake bearer + persona; assert: Chromium child spawns, renderer loads
  the test VRM, control-WS opens, idle frames begin streaming via
  trickle.
- `session-control.smoke.py` — pytest. Send `speak` over control-WS;
  assert: TTS provider called; audio mux + frame stream resume; LLM
  emits a response; `speaking_started`/`speaking_ended` events fire.
- `renderer-snapshot.test.ts` (avatar-renderer) — vitest + jsdom + a
  synthetic VRM. Verify breath / blink / sway controllers update
  properties at expected rates.

### 11.4 Conformance fixtures

`livepeer-network-protocol/conformance/fixtures/session-control-plus-media/`
exists per plan 0012. The vtuber components are the first production
consumers; fixture additions specific to vtuber semantics (per-second
work-units; control-WS event taxonomy) tracked under
plan 0012-followup, not this brief. This brief lifts the existing
fixtures into smoke harnesses.

## 12. Operator runbook deltas

### 12.1 vtuber-gateway/docs/operator-runbook.md (NEW)

1. **Compose deploy** — same as openai-gateway with vtuber-specific
   env. Compose stack adds `vtuber-runner-mock` (a fake runner shim)
   for offline smoke.
2. **First customer + session** — admin SPA → New Customer; portal
   SPA → first API key; portal SPA → New Session (uploads VRM, picks
   persona, picks LLM/TTS providers).
3. **Per-second metering tuning** — `VTUBER_RATE_CARD_USD_PER_SECOND`
   default; override per-offering via admin SPA's
   `admin-vtuber-rate-card`.
4. **Control-WS relay scaling** — `VTUBER_RELAY_MAX_PER_SESSION`
   guides max customer dashboards per session (typically 1, but
   replicas + ops dashboards push it).
5. **Worker-control bearer keys** — gateway holds the private key;
   runner holds the matching public key. Rotation requires runner
   restart.
6. **Session-bearer TTL guidance** — set
   `VTUBER_SESSION_BEARER_TTL_SECONDS` to 2× session-default-TTL so
   long-running sessions don't lose their bearer mid-stream.
7. **Stuck-session sweep** — operator may force-end sessions via
   admin SPA or a SQL UPDATE; implement only the SPA path; SQL is
   power-tool only.

### 12.2 vtuber-pipeline/docs/operator-runbook.md (NEW)

1. **Three binaries; three compose services** — `pipeline-mock-youtube`,
   `pipeline-streams`, `pipeline-egress`. Each takes its own env.
2. **Replacing mock_youtube with real YouTube** — config-flip
   `STREAMS_CHAT_PROVIDER=youtube`; provide YouTube Data API
   credentials.
3. **Egress ffmpeg sizing** — per-stream ~1.5 cores + ~250 MB RAM;
   document per-host concurrency limits.
4. **Provisioning the meta-customer API key on vtuber-gateway** —
   one-time setup per OQ4 lock. On the vtuber-gateway side, admin SPA
   → Customers → New → tier=enterprise / metered / etc → mint key
   (record `LIVEPEER_VTUBER_GATEWAY_API_KEY`); paste into pipeline-app's
   env. Pipeline-app's per-customer billing is its own concern;
   vtuber-gateway sees one customer = pipeline-app.
5. **streams ↔ vtuber-gateway pairing** — each pipeline-streams
   deployment binds to one gateway URL + one shared meta-customer
   API key. Multi-tenant pipeline-app deployments do **not** mint
   per-customer keys on the gateway; they bill internally.

### 12.3 vtuber-runner/docs/operator-runbook.md (NEW)

1. **One runner = one host** — runner spawns one Chromium child per
   session; concurrency capped by host vCPU + RAM.
2. **Chromium installation** — runner Dockerfile bakes Chromium;
   non-Docker installs need `playwright install chromium`.
3. **VRM hosting** — VRMs are customer-uploaded; the runner fetches
   from a URL the gateway hands over. Document URL allowlist policy.
4. **OLV upgrade** — vendored at `third_party/olv/` (per OQ2 lock —
   vendor lift, not git submodule); follow the rebase procedure in
   `third_party/olv/UPSTREAM.md`. Upgrade is a deliberate copy + commit.
5. **Trickle publish endpoint** — broker-issued; runner fails fast if
   `TRICKLE_PUBLISH_URL` unreachable at session-open.

## 13. Migration sequence

5 phases. Phase 1 + 2 (pipeline + runner) can pre-ship — they are not
chain-gated. Phase 3+ are the gateway phases and gate on plan 0016.

### Phase 1 — vtuber-pipeline lift

Copy `livepeer-vtuber-project/pipeline-app/` → `vtuber-pipeline/`.
Rename Python package `pipeline` → `vtuber_pipeline`. Rename
`bridge.py` → `gateway.py`. Add to root uv workspace.

**Acceptance:** `uv sync` green; existing pytest suite passes;
`pipeline-streams`, `pipeline-egress`, `pipeline-mock-youtube`
console scripts run. Pre-1.0.0 shippable.

### Phase 2 — vtuber-runner + avatar-renderer lift

Copy `livepeer-vtuber-project/{session-runner,avatar-renderer}/` →
`vtuber-runner/{src/session_runner, src/avatar_renderer}/`. The
avatar-renderer becomes a sub-workspace (TS) inside
`vtuber-runner/`. Wire the Dockerfile to bundle the renderer dist
into the runtime image.

**Acceptance:** `uv sync` + Vite build green; renderer-snapshot test
passes; session-start smoke passes against a mock LLM/TTS. Pre-1.0.0
shippable. Add to `host-config.yaml` examples in plan 0018 (broker
roster).

### Phase 3 — vtuber-gateway scaffold + DB schema

Create `vtuber-gateway/` workspace package; depend on
`customer-portal/` + `gateway-adapters/`. Land
`migrations/0000_vtuber_init.sql` (vtuber.* namespace).

**Acceptance:** `pnpm -F @livepeer-rewrite/vtuber-gateway build`
green; migrations apply cleanly; admin SPA loads the customer-portal
shell.

### Phase 4 — gateway routes + relay + payments

Port `routes/sessions.ts`, `routes/session-control-ws.ts`, the
relay service, the registry refresher, the per-session payer-daemon
flow. Drop quote-related code (quote-free flow). Drop service-
registry; broker URL is the only resolver.

**Acceptance:** session-open smoke green; control-WS bidirectional;
per-second debits accrue; refunds on error; integration tests cover
the four termination triggers (expires_at, customer end,
insufficient balance, session-bearer revoked).

### Phase 5 — wire cut + suite shell deprecate

Same as 0013-openai phase 4: rename headers; replace
`providers/nodeClient` with `gateway-adapters/`; drop `serviceRegistry`,
`quoteCache`, `quoteRefresher`; swap RPC shape;
re-gen suite gRPC stubs against rewrite proto. Mark suite's
`livepeer-vtuber-gateway` as deprecated; final tag points here.

**Acceptance:** smoke against `capability-broker` +
`session-control-plus-media@v0` driver + `vtuber-runner` (real, not
mock). All four termination triggers pass. Streaming-WS preserves
true pass-through (no buffering on the relay).

## 14. Resolved decisions

User walks 2026-05-06 (Q1-Q10) + 2026-05-07 (OQ1-OQ5); recorded as
`DECIDED:` blocks.

### Q1. Three components vs one mega-component

**DECIDED: three components.** Gateway / pipeline / runner have
different stacks (TS / Python / Python+browser-TS), different release
cadences (gateway is chain-gated, pipeline+runner are not), and
different operator audiences (gateway = SaaS operator; pipeline = SaaS
operator + product engineering; runner = host operator). Collapsing
into one component would force a single Dockerfile + release process
that doesn't fit any of the three.

### Q2. Canonical vtuber-project source

**DECIDED: top-level `livepeer-vtuber-project/`** (not the suite's
mirror submodule at `livepeer-network-suite/livepeer-vtuber-project/`).
Per user-memory `reference_open_llm_vtuber_rendering.md`. The suite
mirror is retired with the rest of the suite; the top-level repo is
absorbed into the rewrite.

### Q3. Python or TypeScript port for pipeline + runner

**DECIDED: keep Python.** Python is the pipeline + runner's
incumbent stack (FastAPI + Pydantic + structlog + httpx + websockets;
av + numpy + aiohttp + msgpack on the runner side). A TS port
buys nothing and costs months. Variance from the canonical TS lock
documented in §6.2 + §6.3.

### Q4. avatar-renderer location

**DECIDED: sub-workspace inside `vtuber-runner/`.** It's strictly
runner-coupled (lifecycle = runner-spawned Chromium child; dist is
bundled into runner image). Lives at `vtuber-runner/src/avatar_renderer/`
with its own `package.json` and Vite config. Not a top-level
component.

### Q5. Schema namespace

**DECIDED: `vtuber.*`.** Per plan 0013-shell Q6 + 0013-openai Q1
patterns. The vtuber-gateway lands its product schema in `vtuber.*`;
no FK out of `app.*`; FKs into `app.customers` only.

### Q6. "bridge" terminology — eradicated

**DECIDED: rename.** Per user-memory `feedback_no_bridge_term.md`,
the suite's "vtuber-livepeer-bridge" is renamed throughout: the
component is `vtuber-gateway/`; the pipeline provider
`bridge.py` is renamed `gateway.py`. `livepeer-vtuber-bridge-mvp.md`
suite docs become `vtuber-gateway-overview.md` style. Citations to
suite paths preserve the historical name (`bridge.py:line`); narrative
text uses "gateway".

### Q7. Per-session payer flow vs per-request

**DECIDED: per-session.** vtuber sessions are long-running (1h
default; per-second metering). Each session opens **one** ticket via
`payerDaemon.createPayment(face_value=session-default,
recipient=node, capability=livepeer:vtuber-session, offering=...)`
and reconciles per-second. This matches the suite's
`vtuberSession.ts:vtuberSessionPayments` flow but quote-free.

### Q8. Worker-control bearer auth scheme

**DECIDED: keep the suite's HMAC + private-key signing scheme.** The
gateway holds the signing key; the runner holds the verification
public key. Suite reference at
`livepeer-vtuber-gateway/src/service/auth/workerControlBearer.ts` ports
unchanged. Bearer is per-session, short-lived.

### Q9. Control-WS relay buffering

**DECIDED: live-only; no replay buffer.** Suite reference at
`livepeer-vtuber-gateway/src/runtime/http/vtuber/relay.ts:24-27`
explicitly notes `events-taxonomy.md` "Reconnect and replay" is
deferred; carry forward. Customer reconnects = `cannot_resume`.

### Q10. mock_youtube — keep as library code or fixture

**DECIDED: keep as `vtuber-pipeline/src/vtuber_pipeline/mock_youtube/`.**
Operators run it as a local YouTube replacement during development
+ test deployments. Production deployments swap to a real YouTube
provider via config flag. Suite ships it as
`pipeline-mock-youtube` console script; carry forward.

### OQ1. avatar-renderer build strategy

**DECIDED: rebuild from source in the runner Dockerfile.** No
pre-built bundle artifact published. Simpler invariant — one source
of truth, no version-drift between a published artifact and the
runner image. Bundle size is small enough the rebuild cost is
negligible. The runner Dockerfile's three-stage build (Vite renderer
→ Python deps via uv → runtime with Playwright + chromium-headless-shell)
does the renderer rebuild as stage 1. See §4.3 + §6.4.

### OQ2. OLV (Open-LLM-VTuber) upstream

**DECIDED: vendor in `third_party/olv/`** (no git submodule). Matches
user-memory `feedback_submodule_url_protocol.md` (HTTPS-only)
preference + the rewrite's clean-slate philosophy. Suite has it
vendored. Submodule complexity (init/update, recursive clone, CI
gotchas) isn't worth it for OLV's slow upstream release cadence. The
vendor lift includes a `third_party/olv/UPSTREAM.md` documenting the
upstream commit hash + rebase procedure for pulling new versions.
See §4.3 + §5.3 + §6.3 + §12.3.

### OQ3. vtuber-gateway portal SPA placement

**DECIDED: ship vtuber-specific pages in
`vtuber-gateway/src/frontend/portal/`.** NOT in `customer-portal/portal/`.
Product-specific pages don't belong in the shared shell. Matches the
per-product separate-businesses framing from plan 0013-shell's OQ3
lock. The shared `customer-portal/frontend/shared/` library provides
common widgets (auth forms, API-key UI, balance display, Stripe
checkout, layout, design tokens); each per-product portal composes
those primitives + adds product-specific routes (vtuber-session list,
persona authoring, scene history).

### OQ4. `pipeline-streams` ↔ `vtuber-gateway` API key shape

**DECIDED: shared-per-deployment (default), with per-customer as
opt-in for direct B2B integrators.** Pipeline-app is a SaaS product
with its own customer-facing surface; pipeline-app's customers do
**not** sign up to vtuber-gateway directly. Pipeline-app holds **one**
API key on vtuber-gateway and acts as a **meta-customer** (B2B
SaaS-on-SaaS relationship). Pipeline-app's per-customer billing is its
own concern; vtuber-gateway sees pipeline as a single high-volume
customer.

For direct B2B integrators of vtuber-gateway (non-pipeline customers
integrating the gateway directly), the standard customer-portal flow
issues per-customer API keys via the gateway's own portal SPA — same
as openai-gateway / video-gateway. Both modes coexist; pipeline-app
integration uses shared-per-deployment, direct integrators use
per-customer.

This **corrects the brief's prior "per-customer (matches
multi-tenancy)" recommendation** in this plan's first version — that
framing forced pipeline-app to manage N vtuber-gateway keys per
pipeline customer, which is operationally awkward for marginal
multi-tenancy benefit. Shared-per-deployment is the cleaner default.
See §3 + §8.1 + §8.2 + §9.2 + §10.2 + §12.2.

### OQ5. Runner work-unit transport mechanism

**DECIDED: `SessionRunnerControl.ReportWorkUnits(stream)` gRPC
bidi-stream** (control-IPC channel between broker and runner,
established in plan 0012-followup §8 + Q8 lock). Runner reports
monotonic deltas; broker accumulates into `atomic.Uint64`; broker's
interim-debit ticker (plan 0015) reads via
`LiveCounter.CurrentUnits()`. **NOT** response trailer (no HTTP
trailer surface for the session-driven mode). **NOT** control-WS
events to the customer (control-WS is broker↔customer; runner-reported
metrics ride the runner-IPC channel that's broker↔runner). Cite plan
0012-followup §8 + plan 0012-followup's resolved Q8.

## 15. Out of scope (forwarding addresses)

- **Customer-portal shell internals** — plan 0013-shell.
- **OpenAI gateway collapse** — plan 0013-openai (vtuber-runner
  *consumes* openai-gateway as an LLM/TTS provider, but their migrations
  are independent).
- **Wire-protocol middleware for `session-control-plus-media@v0`** —
  plan 0008-followup.
- **Broker-side mode driver + media plane** — plan 0012 (closed) +
  plan 0012-followup.
- **Chain integration** — plan 0016 (gateway gate; runner +
  pipeline pre-ship).
- **Real YouTube provider** (replacing mock_youtube) — product-side
  follow-up; out of this paper.
- **Recording / VOD** — vtuber sessions are live-only in v0.1.
- **Multi-language LLM/TTS routing** — single LLM provider per
  session in v0.1; multi-routing is post-1.0 enhancement.
- **VRM marketplace / hosting** — operator concern; out of scope.
- **`livepeer-vtuber-project/` retirement** — user retires manually
  per `migration-from-suite.md`; this brief absorbs the code, not
  the repo lifecycle.
- **`livepeer-byoc/register-capabilities/`** — out of scope per plan
  0018; runners' `GET /options` preserved.
- **`livepeer-byoc/video-generation/`** — not in scope per user lock.

---

## Appendix A — file paths cited

This monorepo:

- `docs/exec-plans/active/0013-shell-customer-portal-extraction.md` — foundation.
- `docs/exec-plans/active/0013-openai-gateway-collapse.md` — sibling.
- `docs/exec-plans/completed/0012-session-control-plus-media-driver.md` — broker mode.
- `docs/exec-plans/active/0012-followup-session-control-media-plane.md` — parallel work.
- `docs/exec-plans/active/0008-followup-gateway-adapters-non-http-modes.md` — parallel work.
- `docs/exec-plans/active/0016-chain-integrated-payment-design.md` — chain gate (gateway only).
- `docs/exec-plans/active/0018-orch-coordinator-design.md` — host-config.yaml semantics
  (broker scrapes runners' `/options`).
- `livepeer-network-protocol/headers/livepeer-headers.md` — header set.

Suite paths cited:

- `livepeer-network-suite/livepeer-vtuber-gateway/src/main.ts`
- `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/http/vtuber/sessions.ts:1-80,55-66,68-80`
- `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/http/vtuber/relay.ts:1-60,24-27,29-48`
- `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/http/{admin,billing,stripe}/...`
- `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/http/middleware/{auth,rateLimit}.ts`
- `livepeer-network-suite/livepeer-vtuber-gateway/src/service/auth/{sessionBearer,workerControlBearer}.ts`
- `livepeer-network-suite/livepeer-vtuber-gateway/src/service/payments/vtuberSession.ts`
- `livepeer-network-suite/livepeer-vtuber-gateway/src/service/billing/vtuberBilling.ts`
- `livepeer-network-suite/livepeer-vtuber-gateway/src/service/nodes/{vtuberRegistry,registryRefresher,scheduler,nodebook,circuitBreaker,quoteRefresher}.ts`
- `livepeer-network-suite/livepeer-vtuber-gateway/src/providers/workerClient.ts`
- `livepeer-network-suite/livepeer-vtuber-gateway/src/repo/{vtuberSessions,vtuberSessionBearers,nodeHealth,usageRecords,usageRollups}.ts`
- `livepeer-network-suite/livepeer-vtuber-gateway/migrations/{0007_vtuber_sessions,0008_vtuber_session_bearers,0009_vtuber_session_payer_work_id}.sql`

vtuber-project paths cited (canonical top-level):

- `livepeer-vtuber-project/pipeline-app/pyproject.toml:[project.scripts]` — three console scripts.
- `livepeer-vtuber-project/pipeline-app/src/pipeline/{mock_youtube,streams,egress}/runtime/entrypoint.py`
- `livepeer-vtuber-project/pipeline-app/src/pipeline/streams/providers/{bridge,youtube,egress_admin}.py`
- `livepeer-vtuber-project/pipeline-app/src/pipeline/egress/providers/ffmpeg_runner.py`
- `livepeer-vtuber-project/session-runner/pyproject.toml:[project.scripts]`
- `livepeer-vtuber-project/session-runner/src/session_runner/runtime/{entrypoint,app,session_factory}.py`
- `livepeer-vtuber-project/session-runner/src/session_runner/service/{manager,session_pipeline,pipeline,conversation,emotion_mapper,renderer,renderer_chromium,renderer_factory,control_ws,control_dispatcher,channels,output_sink,audio_mux_sink,mux_pipeline,egress_segment_sink,trickle_sink}.py`
- `livepeer-vtuber-project/session-runner/src/session_runner/providers/{llm_livepeer,llm_mock,tts_livepeer,tts_mock,trickle,egress_publisher,olv_loader,telemetry,vector_log}.py`
- `livepeer-vtuber-project/session-runner/src/session_runner/ui/http.py`
- `livepeer-vtuber-project/session-runner/src/session_runner/types/{api,media,state}.py`
- `livepeer-vtuber-project/session-runner/src/session_runner/config/settings.py`
- `livepeer-vtuber-project/avatar-renderer/{package.json,tsconfig.json,vite.config.ts,vitest.config.ts,index.html}`
- `livepeer-vtuber-project/avatar-renderer/src/main.ts:1-24` — query-param + status-reporting docs.
- `livepeer-vtuber-project/Grifter_Squaddie__1419.vrm` — test VRM.
