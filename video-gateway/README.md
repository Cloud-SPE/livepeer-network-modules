# video-gateway

Customer-facing video product gateway for the Livepeer network rewrite —
live streams (RTMP ingest + LL-HLS playback) and VOD (tus upload +
transcode + ABR + signed playback URLs).

> **For agents:** start at [`AGENTS.md`](./AGENTS.md).

## What it is

A TypeScript Fastify service that:

- Exposes `POST /v1/live/streams` to allocate a customer-facing RTMP push
  URL (broker-hosted listener) plus an LL-HLS playback URL.
- Exposes `POST /v1/uploads` (tus protocol) for resumable VOD uploads.
- Submits transcode jobs to the capability-broker via
  `http-reqresp@v0` / `http-stream@v0` modes.
- Forwards LL-HLS playlist + segment requests strictly to the broker
  (no rewrites, no caching — operator's CDN handles those concerns).
- Selects brokers either from a static `LIVEPEER_BROKER_URL` or a
  manifest-driven `service-registry-daemon` resolver socket.
- Signs outbound customer webhook deliveries with HMAC-SHA-256.
- Serves a customer portal at `/portal/` and an operator console at `/admin/console/`.

## What it is not

- A live transcoder. The broker + workload runners do the encoding.
- A CDN. Operators front the gateway with CloudFront / Fastly /
  Cloudflare for caching + edge replication + geographic routing.
- A storage origin. S3-compat (RustFS / AWS S3) holds asset
  bytes; the gateway only orchestrates.

## Status

**v0.1** — collapse complete; awaits live wire-protocol smoke. Plan
[0013-video](../docs/exec-plans/completed/0013-video-gateway-migration.md).

## Build + smoke

Per core belief #15, every gesture is Docker-first.

```bash
make build               # build the video-gateway image
make smoke               # spin up compose stack + run smoke test
make help                # show all targets
```

No host `node` install required.

## Login surfaces

- `/admin/console/` is the operator admin UI. It requires `VIDEO_GATEWAY_ADMIN_TOKENS`
  to be set on the gateway, and the browser login form sends:
  - `Authorization: Bearer <admin-token>`
  - `X-Actor: <operator-name>`
- `/portal/` is the customer UI. It uses customer auth tokens issued by the
  customer-portal subsystem and validated with `CUSTOMER_PORTAL_PEPPER`.

`VIDEO_GATEWAY_ADMIN_TOKENS` and `CUSTOMER_PORTAL_PEPPER` are different concerns:

- `VIDEO_GATEWAY_ADMIN_TOKENS` gates operator-only `/admin/*` routes.
- `CUSTOMER_PORTAL_PEPPER` hashes and verifies customer login tokens for `/portal/login`.

Layer 3 route-health operator endpoints:

- `GET /admin/video/resolver-candidates`
- `GET /admin/video/route-health/metrics`

Interpretation:

- `service-registry-daemon` supplies only routes that survive signed-manifest
  and broker live-health checks
- `/admin/video/resolver-candidates` shows that resolver-backed set plus the
  gateway's local Layer 3 summary and per-route state
- `/admin/video/route-health/metrics` exposes the same cooldown and outcome
  counters in Prometheus text format

## Layout

```
video-gateway/
├── package.json                 # @livepeer-network-modules/video-gateway
├── tsconfig.json
├── Dockerfile / Makefile
├── compose.yaml                 # gateway only
├── compose.smoke.yaml           # full stack (postgres + redis + payment-daemon + broker + gateway)
├── migrations/
│   └── 0000_video_init.sql      # media.assets / encoding_jobs / live_streams / playback_ids / renditions / uploads / pricing / webhooks / recordings
├── src/
│   ├── engine/                  # ported video-core engine (cost quoter, encoding planner, manifest builder, playback URL builder, webhook signer, dispatchers)
│   ├── routes/                  # /v1/live/streams /v1/vod /v1/uploads /v1/playback /v1/projects /v1/webhooks /_hls/...
│   ├── service/                 # business logic (live, vod, billing, health)
│   ├── repo/                    # drizzle-backed media.* repos
│   ├── adapters/                # postgres + redis + S3 + webhooks + wallet
│   ├── middleware/              # auth + rate-limit + request logging
│   ├── livepeer/                # wire layer (headers + payment + RTMP adapter + capability map)
│   ├── runtime/rtmp/            # pure-TS RTMP listener (customer-facing)
│   ├── pricing/                 # product-specific rate-card (live per-minute + VOD per-resolution)
│   ├── frontend/web-ui/         # operator admin SPA (Lit)
│   ├── config.ts                # Zod env
│   ├── server.ts                # Fastify factory
│   └── index.ts                 # entry point
├── test/
│   ├── integration/
│   └── smoke/
└── docs/
```
