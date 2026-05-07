# AGENTS.md

This is `video-gateway/` — the customer-facing video product gateway for
the Livepeer network rewrite. Customer encoders push RTMP to the broker
(via this gateway's session-open surface) for live streams; tus uploads
land here for VOD; the gateway dispatches transcode jobs to runners
through the capability-broker. Plan
[0013-video](../docs/exec-plans/completed/0013-video-gateway-migration.md)
collapses the suite's `livepeer-video-gateway` shell + `livepeer-video-core`
engine into this single rewrite component.

Component-local agent map. Repo-root [`../AGENTS.md`](../AGENTS.md) is the
cross-cutting map.

## Operating principles

Inherited from the repo root. Plus:

- **Single-package collapse.** No `-core` engine + shell split. The
  `livepeer-video-core@0.0.1-dev` package was never published; its
  source merges into this component (plan 0013-video §14 Q4 lock).
- **Consume customer-portal, do not duplicate it.** Workspace dep
  `@livepeer-rewrite/customer-portal` is the only source of customer
  identity, ledger movement, Stripe webhook handling, admin SPA, and
  shared middleware. New SaaS surfaces land in customer-portal/, not
  here.
- **Schema namespace strict-split.** This component owns `media.*`;
  customer-portal owns `app.*`. Suite mixed both in one migration; the
  rewrite separates ownership (plan 0013-video §14 Q3 lock).
- **Strict-proxy LL-HLS playback.** The gateway forwards customer
  playback requests to the broker's LL-HLS handler with no
  cache-control / CORS / playlist-rewrite logic. CDN concerns are
  operator add-on (plan 0013-video §14 OQ1 lock).
- **VOD soft-delete only in v0.1.** `DELETE /v1/videos/assets/{id}`
  flips `media.assets.deleted_at`; hard-delete + S3 cleanup defers to
  a future janitor-job plan (plan 0013-video §14 OQ2 lock).
- **Customer-tier ABR ladder.** `VIDEO_GATEWAY_ABR_POLICY=customer-tier`
  is the only v0.1 policy; the customer's billing tier picks the ladder
  at session-open (plan 0013-video §14 OQ3 lock).
- **VOD recording opt-in per-stream.** `record_to_vod: true` in
  session-open params triggers the live → VOD handoff at session end;
  default off (plan 0013-video §14 OQ4 lock).
- **Pure-TS RTMP listener.** Customer-side RTMP termination is pure
  Node + a TS RTMP lib; no Go sidecar (plan 0013-video §14 Q1 lock).
- **Mainnet only.** No testnets. Smoke deploys against Arbitrum One.

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
| The collapse plan | [`../docs/exec-plans/completed/0013-video-gateway-migration.md`](../docs/exec-plans/completed/0013-video-gateway-migration.md) |

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
- **video-gateway owns**: live-stream session orchestration (RTMP
  session-open + LL-HLS strict-proxy + stuck-session sweep), VOD pipeline
  (tus upload + encoding-job dispatch + ABR ladder + manifest builder +
  playback URL builder + cost quoter), customer-side webhook signing
  (HMAC-SHA-256), product-specific admin SPA extras (live + VOD pricing
  pages), Livepeer wire headers + payment minting + mode dispatch.

## Doing work in this component

- Docker-first per core belief #15. Use `make build`, `make smoke`.
- TypeScript strict; tsc is the lint gate.
- Migrations boot in order: customer-portal/migrations/ first, then
  video-gateway/migrations/. The shell's `runMigrations(db, dir)` helper
  is called twice with the two paths from `src/main.ts`.
- Suite-citation paths in commit messages must match
  `livepeer-network-suite/livepeer-video-{gateway,core}/...` verbatim
  per the repo-root AGENTS.md attribution convention.
