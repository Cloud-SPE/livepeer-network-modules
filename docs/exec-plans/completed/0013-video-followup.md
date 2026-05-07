---
plan: 0013-video-followup
title: video-gateway adapter impls + retry policy + payment activation + dual SPAs — design
status: completed
phase: closed
opened: 2026-05-07
closed: 2026-05-07
owner: harness
related:
  - "completed plan 0013-video — video-gateway suite-collapse (parent)"
  - "completed plan 0011-followup — broker-side RTMP listener + LL-HLS server"
  - "completed plan 0008-followup — gateway-adapters non-HTTP modes (RTMP middleware)"
  - "completed plan 0013-shell — customer-portal shell (auth/Stripe/middleware/Lit shared)"
  - "completed plan 0016 — chain-integrated payment-daemon (chain gate; already shipped)"
  - "active plan 0015 — interim-debit cadence (LiveCounter contract)"
audience: video-gateway maintainers landing the production-grade follow-up
---

# Plan 0013-video-followup — adapter impls + webhook retry + payment wire + dual SPAs (design)

> **Paper-only design brief.** Locks recorded in §14 as `DECIDED:`
> blocks. Implementing agent works against these locks; cadence in §13.

## 1. Status and scope

Scope: **the production-grade follow-up to plan 0013-video.** The parent
shipped scaffold, schema, engine merge, RTMP/HLS routes, soft-delete +
HMAC + recording, and a compose smoke. What remains:

1. **Concrete repo adapters.** `assetRepo` / `liveStreamRepo` /
   `webhookRepo` / `recordingRepo` are interface-only stubs in
   `video-gateway/src/engine/repo/`. This plan lands drizzle-orm impls
   against the already-shipped `media.*` schema.
2. **Concrete `s3StorageProvider`.** The interface is defined; no
   provider exists. tus-upload + asset-playback presigned URLs need
   one.
3. **Webhook retry + dead-letter policy.** The current
   `webhookDispatcher` does a single POST. Land 3-retry exponential
   backoff + `media.webhook_failures` dead-letter table + operator
   replay.
4. **Wire-layer payment activation.** `livepeer/payment.ts` returns an
   empty header. Activate against the payer-daemon unix socket.
5. **Dual SPA fill-in.** Operator admin SPA already scaffolded under
   `src/frontend/web-ui/`; fill in customer-lookup, balance-adjust,
   asset-library, live-stream inspect, webhook audit/replay,
   refunds. **NEW:** customer-facing portal SPA at
   `src/frontend/portal/` composes customer-portal/shared widgets +
   adds video-specific routes (assets, streams, webhooks, recordings).

Out of scope:

- Broker-side RTMP/FFmpeg/HLS pipeline — closed by 0011-followup.
- Gateway-adapters middleware for `rtmp-ingress-hls-egress@v0` —
  closed by 0008-followup.
- Hard-delete janitor for VOD assets — separate retention plan.
- WebRTC/DASH egress — out of scope per parent §15.
- Chain-side payment lifecycle — chained behind plan 0016 (shipped).
- Customer-portal shell internals — owned by 0013-shell.
- Operator-flat-rate ABR policy — future minor expansion of
  `VIDEO_GATEWAY_ABR_POLICY`.

## 2. What the parent plan left unfinished

Parent 0013-video closed with 14 DECIDED locks (§14 in the parent),
shipped:

- §13 Phase 1 — workspace scaffold + DB + Fastify + Postgres pool.
- §13 Phase 2 — engine merge (dispatch/service/types).
- §13 Phase 3 — RTMP session-open + LL-HLS strict-proxy + tus VOD.
- §13 Phase 4 — customer-tier ABR + soft-delete + webhook signing +
  recording.
- §13 Phase 5 — compose smoke + plan-close.

What landed as **stubs** rather than impls:

- `video-gateway/src/engine/repo/{assetRepo,liveStreamRepo,uploadRepo,playbackIdRepo,jobRepo}.ts` —
  interfaces only; no Postgres-backed concrete factories.
- `video-gateway/src/engine/interfaces/{webhookSink,storageProvider}.ts` —
  contracts only; no concrete impls.
- `video-gateway/src/livepeer/payment.ts` — `buildPayment` returns
  `{ header: "" }`.
- `video-gateway/src/service/webhookDispatcher.ts` — single POST, no
  retry, no dead-letter row.
- `video-gateway/src/frontend/web-ui/` — `index.html` + `main.ts` +
  empty `admin-app.ts` placeholder.
- Customer-facing portal — not scaffolded at all.

## 3. Reference architecture

```
   customer browser ─── customer-portal/frontend/shared widgets
     │                  + video-specific routes
     ▼
   ┌────────────────────────────────────────────────────────┐
   │  video-gateway/src/frontend/portal/  (NEW SPA)         │
   │   /portal/assets       VOD library + tus upload        │
   │   /portal/streams      live-stream create + inspect    │
   │   /portal/webhooks     register URLs + signing-secret  │
   │   /portal/recordings   opt-in record_to_vod toggle     │
   │   /portal/{api-keys,billing,account}  shell-shared     │
   └────────────────────────────────────────────────────────┘
                                │
   operator browser ────────────┴────────────────────────────
     │
     ▼
   ┌────────────────────────────────────────────────────────┐
   │  video-gateway/src/frontend/web-ui/  (admin SPA)        │
   │   /admin/customers    lookup + balance + refund        │
   │   /admin/assets       VOD library + soft-delete toggle │
   │   /admin/streams      session inspect + force-end      │
   │   /admin/webhooks     delivery audit + dead-letter     │
   │                       replay                           │
   │   /admin/workers      pool inspection                  │
   └────────────────────────────────────────────────────────┘
                                │
                                ▼
   ┌────────────────────────────────────────────────────────┐
   │  video-gateway Fastify (Node)                          │
   │   ├─ src/repo/{assets,liveStreams,webhooks,recordings} │
   │   │     drizzle-orm CRUD on media.*                    │
   │   ├─ src/storage/s3.ts                                 │
   │   │     @aws-sdk/client-s3 — endpoint config knob      │
   │   │     for AWS S3 / RustFS / R2                       │
   │   ├─ src/service/webhookDispatcher.ts                  │
   │   │     3-retry backoff (1s/5s/30s) → dead-letter      │
   │   └─ src/livepeer/payment.ts                           │
   │         payerDaemon.createPayment over unix socket     │
   └────────────────────────────────────────────────────────┘
                                │
                                ▼
              payment-daemon (unix sock) — dev or prod
              capability-broker (broker:1935 / broker /v1/cap)
              S3-compatible operator storage
```

## 4. Component layout deltas

```
video-gateway/
  migrations/
    0005_webhook_failures.sql        ← NEW (dead-letter table)
  src/
    repo/                            ← NEW dir (concrete drizzle adapters)
      assets.ts
      liveStreams.ts
      webhooks.ts
      recordings.ts
      index.ts
    storage/                         ← NEW dir (s3 provider)
      s3.ts
      index.ts
    livepeer/
      payment.ts                     ← rewrite (real payerDaemon call)
      payerDaemonClient.ts           ← NEW (unix-socket client)
    service/
      webhookDispatcher.ts           ← rewrite (retry + dead-letter)
    frontend/
      web-ui/                        ← FILL-IN (admin pages)
        components/
          admin-app.ts               ← rewrite (router + shell)
          admin-customers.ts         ← NEW
          admin-assets.ts            ← NEW
          admin-streams.ts           ← NEW
          admin-webhooks.ts          ← NEW
          admin-workers.ts           ← NEW
      portal/                        ← NEW SPA (sub-workspace)
        package.json
        README.md
        src/
          index.html
          main.ts
          components/
            portal-assets.ts
            portal-streams.ts
            portal-webhooks.ts
            portal-recordings.ts
  test/
    repo/
      assets.test.ts                 ← NEW
      liveStreams.test.ts            ← NEW
      webhooks.test.ts               ← NEW
      recordings.test.ts             ← NEW
    storage/
      s3.test.ts                     ← NEW
    service/
      webhookDispatcher.retry.test.ts ← NEW
```

## 5. Concrete drizzle adapters (Phase 2)

`AssetRepo` / `LiveStreamRepo` / `WebhookRepo` / `RecordingRepo`
factories — match `customer-portal/src/repo/customers.ts` shape.

**Pattern (mirrors `customer-portal/src/repo/customers.ts`):**

```ts
export function createAssetRepo(db: Db): AssetRepo {
  return {
    async insert(asset) { /* drizzle insert */ },
    async byId(id)      { /* drizzle select limit 1 */ },
    async list(opts)    { /* deletedAt filter unless includeDeleted */ },
    async softDelete(id, at) { /* update set deletedAt */ },
    /* ... */
  };
}
```

Soft-delete handling: filter `deleted_at IS NULL` unless `?include_deleted=true`.
The `softDelete()` flips `deleted_at = now()`.

Engine repos (`engine/repo/*.ts`) are interface-only and stay
interface-only; the new `src/repo/*.ts` files implement them. Wired
in `src/index.ts` factory (parent §10 already adds `videoDb`).

## 6. `s3StorageProvider` (Phase 3)

`@aws-sdk/client-s3` + `@aws-sdk/s3-request-presigner` — official AWS
SDK v3, modular tree-shakeable bundles. Hand-rolled HTTP-S3 client
rejected (Q2 lock).

Provider config (env): `S3_ENDPOINT`, `S3_REGION`, `S3_BUCKET`,
`S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY`. Endpoint config knob makes
AWS / RustFS / Cloudflare R2 / any S3-compatible backend work.

Surface (matches `engine/interfaces/storageProvider.ts`):

- `putSignedUploadUrl({assetId,kind,filename,contentType,expiresInSec})` →
  presigned `PUT` URL via `getSignedUrl(client, PutObjectCommand)`.
- `getSignedDownloadUrl({storageKey,expiresInSec})` → presigned `GET`
  URL via `getSignedUrl(client, GetObjectCommand)`.
- `putObject(storageKey, body, {contentType?})` → `PutObjectCommand`.
- `delete(storageKey)` → `DeleteObjectCommand`.
- `copyObject(srcKey, dstKey)` → `CopyObjectCommand`.
- `pathFor({assetId?,streamId?,kind,codec?,resolution?,filename})` →
  string key derivation per `StoragePathKind`.

Multi-part uploads handled by AWS SDK's `Upload` from
`@aws-sdk/lib-storage` for parts >5MB (tus chunks). Tests use a local
RustFS compose service.

## 7. Webhook retry + dead-letter (Phase 4)

New migration `0005_webhook_failures.sql` — `media.webhook_failures`
table:

```sql
CREATE TABLE media.webhook_failures (
  id                 TEXT PRIMARY KEY,
  endpoint_id        TEXT NOT NULL REFERENCES media.webhook_endpoints(id) ON DELETE CASCADE,
  delivery_id        TEXT NOT NULL,
  event_type         TEXT NOT NULL,
  body               TEXT NOT NULL,
  signature_header   TEXT NOT NULL,
  attempt_count      INTEGER NOT NULL,
  last_error         TEXT NOT NULL,
  status_code        INTEGER,
  dead_lettered_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  replayed_at        TIMESTAMPTZ
);
CREATE INDEX webhook_failures_endpoint ON media.webhook_failures (endpoint_id);
CREATE INDEX webhook_failures_dead_lettered ON media.webhook_failures (dead_lettered_at);
```

Retry policy in `webhookDispatcher.ts`:

| Attempt | Wait before send | Trigger to retry |
|---|---|---|
| 1 | 0 | initial send |
| 2 | 1s | 5xx OR network error |
| 3 | 5s | 5xx OR network error |
| 4 | 30s | 5xx OR network error |
| (after) | dead-letter | mark `failed`; insert `webhook_failures` row |

4xx status codes are **customer-error**: skip retries, dead-letter
immediately. HMAC-SHA-256 signing unchanged from parent Q7.

Operator replay: `POST /admin/webhook-failures/:id/replay` re-runs
the 3-retry policy with the saved body+signature; on success sets
`replayed_at`.

## 8. Wire-layer payment activation (Phase 5)

`buildPayment(input)` calls `payerDaemon.createPayment({ faceValueWei,
recipient, capability, offering, nodeId })` over the unix socket at
`LIVEPEER_PAYER_DAEMON_SOCKET`. Shape matches
`vtuber-gateway/src/providers/payerDaemon.ts` + `service/payments/createPayment.ts`.

Per Q5 lock: **no feature flag.** Daemon's `--chain-rpc` flag governs
dev-vs-prod payment bytes. In dev (no `--chain-rpc`), daemon returns
fake bytes; in prod, real chain-anchored bytes. The gateway is
agnostic.

Returns `{ header: paymentHeader, workId: payerWorkId }`.

## 9. Operator admin SPA fill-in (Phase 6)

`src/frontend/web-ui/`:

| Page | Route | Composes |
|---|---|---|
| `admin-customers` | `/admin/customers` | customer-portal `<lp-customer-search>`, `<lp-balance-display>`; balance-adjust + refund actions |
| `admin-assets` | `/admin/assets` | `media.assets` table view + soft-delete toggle |
| `admin-streams` | `/admin/streams` | `media.live_streams` table; per-row status + force-end button |
| `admin-webhooks` | `/admin/webhooks` | per-customer delivery log + `media.webhook_failures` table + replay action |
| `admin-workers` | `/admin/workers` | which orchs/brokers serve what offerings |

Composes `customer-portal/frontend/shared` widgets. Lit + RxJS.
Auth: route guard via customer-portal's admin-auth controller.

## 10. Customer portal SPA (Phase 7)

`src/frontend/portal/` — NEW pnpm sub-workspace
`@livepeer-rewrite/video-gateway-portal`. Mirrors
`vtuber-gateway/src/frontend/portal/` shape.

| Page | Route | Notes |
|---|---|---|
| `portal-assets` | `/portal/assets` | VOD asset library (list/view, tus upload widget, soft-delete UI) |
| `portal-streams` | `/portal/streams` | live-stream creation flow (RTMP URL + LL-HLS playback URL display; viewer count + per-stream billing) |
| `portal-webhooks` | `/portal/webhooks` | customer registers webhook URLs + sees signing-secret one-time-reveal + delivery audit (mirrors API-keys reveal pattern) |
| `portal-recordings` | `/portal/recordings` | per-stream `record_to_vod` toggle + recording list |

Shell-shared (composed from `customer-portal-shared`): `/portal/api-keys`,
`/portal/billing`, `/portal/account`.

Update root `pnpm-workspace.yaml` to add
`video-gateway/src/frontend/portal`.

## 11. Configuration surface deltas

| Env var | Required | Purpose |
|---|---|---|
| `S3_ENDPOINT` | yes (prod) | S3-compatible endpoint URL. |
| `S3_REGION` | yes | e.g. `us-east-1`. |
| `S3_BUCKET` | yes | Bucket name. |
| `S3_ACCESS_KEY_ID` | yes | Static creds (or IAM role on AWS). |
| `S3_SECRET_ACCESS_KEY` | yes | Static creds. |
| `S3_FORCE_PATH_STYLE` | no (default true) | RustFS needs path-style; AWS uses virtual-host. |
| `WEBHOOK_RETRY_BACKOFFS_SECONDS` | no (default `1,5,30`) | Comma-separated backoff schedule. |

`LIVEPEER_PAYER_DAEMON_SOCKET` already documented in parent §10.

## 12. Conformance / smoke tests

### 12.1 Unit (per phase)

| Phase | Test file | Coverage |
|---|---|---|
| 2 | `test/repo/{assets,liveStreams,webhooks,recordings}.test.ts` | CRUD + soft-delete + filter |
| 3 | `test/storage/s3.test.ts` | presigned URL shape; round-trip via local RustFS |
| 4 | `test/service/webhookDispatcher.retry.test.ts` | retry-then-success / retry-then-dead-letter / 4xx-immediate / replay |
| 5 | `test/livepeer/payment.test.ts` | mock payer-daemon returns canned `paymentHeader`; gateway forwards |
| 6 | (smoke widget render via jsdom) | admin-app routes resolve |
| 7 | (smoke widget render via jsdom) | portal-app routes resolve |

### 12.2 Compose smoke

`compose.smoke.yaml` adds:
- `rustfs/rustfs:latest` service (S3-compatible)
- `mock-payer-daemon` (canned `createPayment` response)

Existing parent compose entries keep `tztcloud/livepeer-*:v0.8.10` pin.

## 13. Migration sequence

8 commits.

1. **`docs(plan-0013-video-followup): author brief with 5 DECIDED locks`** —
   this file. Phase 1.
2. **`feat(video-gateway): drizzle-orm adapters for assets / streams / webhooks / recordings`** —
   Phase 2.
3. **`feat(video-gateway): s3StorageProvider via @aws-sdk/client-s3`** —
   Phase 3.
4. **`feat(video-gateway): webhook retry policy + dead-letter table`** —
   Phase 4. New `0005_webhook_failures.sql`.
5. **`feat(video-gateway): activate wire-layer payment via payer-daemon`** —
   Phase 5.
6. **`feat(video-gateway): operator admin SPA fill-in`** — Phase 6.
7. **`feat(video-gateway): customer-facing portal SPA (assets/streams/webhooks/recordings)`** —
   Phase 7. Adds `pnpm-workspace.yaml` entry.
8. **`docs(plan-0013-video-followup): plan close`** — Phase 8. Move
   `active/` → `completed/`. **PLANS.md untouched** per harness lock.

## 14. Resolved decisions

User walks 2026-05-07; recorded as `DECIDED:` blocks.

### Q1. Concrete adapter backend

**DECIDED: drizzle-orm + Postgres** for `assetRepo` /
`liveStreamRepo` / `webhookRepo`. Same pattern as
`customer-portal/src/repo/`. Schema migrations are already shipped
under `media.*`; just write the drizzle wrappers. Match
`customer-portal/src/repo/customers.ts` shape (factory + per-method
exports). No alternative considered; this is the canonical lock for
the rewrite.

### Q2. `s3StorageProvider` impl

**DECIDED: `@aws-sdk/client-s3`** (AWS SDK v3, modular tree-shakeable
bundles) + `@aws-sdk/s3-request-presigner` for presigned URLs.
Compatible with AWS S3 / RustFS / Cloudflare R2 / any
S3-compatible operator-chosen backend via the `S3_ENDPOINT` config
knob. Hand-rolled HTTP-S3 client rejected — reinvents auth/signing
(SigV4) for marginal savings; SDK is small enough at ~140 KB modular.

### Q3. Webhook retry / dead-letter policy

**DECIDED: 3 retries with exponential backoff (1s / 5s / 30s), then
dead-letter to a new `media.webhook_failures` table.** Operator can
replay dead-lettered webhooks via admin SPA (Phase 6).
4xx → immediate dead-letter (customer-error, retrying won't help).
5xx + network errors → retry. New migration
`0005_webhook_failures.sql` lands in this followup. HMAC-SHA-256
signing unchanged from parent §14 Q7.

### Q4. Frontend SPA scope (REFRAMED — customer-facing IS in scope for v0.1)

**DECIDED: ship BOTH operator admin SPA AND customer-facing portal SPA.**

- `video-gateway/src/frontend/web-ui/` — operator/admin SPA already
  scaffolded in parent plan; fill in customer lookup, balance
  adjustment, webhook delivery audit, asset library browse,
  live-stream session inspection, refunds, dead-letter webhook
  replay.
- `video-gateway/src/frontend/portal/` — **NEW customer-facing SPA.**
  Composes customer-portal's shared widgets (signup / login / API-key
  UI / balance / Stripe checkout / layout) + adds video-specific
  routes:
  - `/portal/assets` — VOD asset library (list, view, tus upload
    widget, soft-delete UI)
  - `/portal/streams` — live-stream management (create stream → get
    RTMP URL + LL-HLS playback URL; viewer count + billing per stream)
  - `/portal/webhooks` — customer registers webhook URLs + sees
    signing-secret + delivery audit
  - `/portal/recordings` — opt-in recordings (per-stream
    `record_to_vod` flag visible in UI)

Both SPAs are pnpm sub-workspaces. Update root `pnpm-workspace.yaml`
to add `video-gateway/src/frontend/portal`.

### Q5. Wire-layer payment activation

**DECIDED: full activation, no feature flag.** Plan 0016's chain
integration already shipped + payer-daemon runs in dev-mode without
`--chain-rpc`. `buildPayment` calls real
`payerDaemon.createPayment({ faceValueWei, recipient: orch.eth_address,
capability, offering, nodeId })` over the unix socket; the daemon's
existing dev-vs-prod split (chain-rpc set or not) governs whether
the bytes are real or fake. No `PAYMENT_ENABLED` flag needed.

## 15. Out of scope (forwarding addresses)

- **Hard-delete janitor for VOD assets** — separate retention plan;
  parent §15.
- **Operator-flat-rate ABR policy** — future minor expansion of
  `VIDEO_GATEWAY_ABR_POLICY`; parent §14 OQ3.
- **WebRTC / DASH egress** — parent §15.
- **DRM / token-gated playback** — operator concern.
- **Multi-region playback** — operator CDN.
- **Per-segment HLS metering** — broker `/_hls` is "free" once
  session-open is paid (plan 0011-followup §6.3).
- **Webhook retry policy beyond 3 attempts** — operator can replay
  dead-letters; long-term retry storms belong in a queue.
- **Operator-side SSO for the admin SPA** — customer-portal's
  admin-auth controller lock; future plan if needed.

---

## Appendix A — file paths cited

This monorepo:

- `docs/exec-plans/completed/0013-video-gateway-migration.md` (parent;
  §14 has 14 prior DECIDED locks).
- `docs/exec-plans/completed/0011-followup-rtmp-media-pipeline.md`
  (broker-side RTMP/HLS; §6.3 LL-HLS handler).
- `docs/exec-plans/completed/0008-followup-gateway-adapters-non-http-modes.md`
  (gateway-adapters middleware).
- `customer-portal/src/repo/customers.ts` (drizzle adapter shape ref).
- `customer-portal/frontend/shared/` (Lit + RxJS shared widgets).
- `customer-portal/frontend/portal/` + `customer-portal/frontend/admin/`
  (SPA scaffold patterns).
- `vtuber-gateway/src/providers/payerDaemon.ts` (payer-daemon client
  shape).
- `vtuber-gateway/src/service/payments/createPayment.ts`
  (createPayment flow).
- `vtuber-gateway/src/frontend/portal/` (customer SPA scaffold
  pattern).
- `video-gateway/src/engine/repo/{asset,liveStream,upload,playbackId,job}Repo.ts`
  (interfaces this plan implements).
- `video-gateway/src/engine/interfaces/{webhookSink,storageProvider}.ts`
  (contracts this plan implements).
- `video-gateway/src/livepeer/payment.ts` (stub this plan replaces).
- `video-gateway/src/service/webhookDispatcher.ts` (dispatcher this
  plan extends with retry).
- `video-gateway/src/db/schema.ts` (drizzle schema for `media.*`).
- `video-gateway/migrations/{0000_video_init,0001_pricing,0002_live_session_debits,0003_webhook_endpoints,0004_recordings}.sql`
  (already shipped).
- `video-gateway/migrations/0005_webhook_failures.sql` (NEW; this
  plan).
- `video-gateway/src/frontend/web-ui/` (admin SPA scaffold; this plan
  fills in).
- `video-gateway/src/frontend/portal/` (NEW; customer SPA).
- `pnpm-workspace.yaml` (this plan adds the portal sub-workspace).
