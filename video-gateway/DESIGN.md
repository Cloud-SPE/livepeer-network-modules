# video-gateway — design

Architectural overview. Plan
[0013-video](../docs/exec-plans/completed/0013-video-gateway-migration.md)
is the canonical design doc.

## Component shape

The video-gateway is a single TypeScript Fastify service that absorbs
two suite repos:

1. `livepeer-network-suite/livepeer-video-gateway/` — Cloud-SPE shell
   (Fastify API + Lit admin SPA). The `apps/playback-origin/` stub is
   discarded.
2. `livepeer-network-suite/livepeer-video-core/` — engine (framework-free
   dispatchers, providers, services). Merges into `src/engine/` as
   source code (the npm package `@cloudspe/video-core@0.0.1-dev` was
   never published).

The collapse mirrors `0013-openai`: one OSS-MIT package
(`video-gateway/`), depends on `customer-portal/` for SaaS surfaces and
`gateway-adapters/` for wire-protocol middleware.

## Reference architecture

```
   customer encoder (OBS, ffmpeg, mux/twitch-style push)
     │  rtmp://broker:1935/<session_id>/<stream_key>
     │  (gateway returns broker URL at session-open)
     ▼
   ┌──────────────────────────┐         ┌─────────────────────────┐
   │  video-gateway/          │  HTTPS  │  capability-broker/     │
   │   - SaaS surfaces        │────────▶│   modes:                │
   │   - REST + WS routes     │         │     rtmp-ingress-       │
   │   - tus VOD upload       │         │       hls-egress@v0     │
   │   - LL-HLS strict-proxy  │◀────────│     http-reqresp@v0     │
   │   - HMAC webhook signer  │   HLS   │     http-stream@v0      │
   └──────────────────────────┘         └─────────────────────────┘
                                                  │
                                                  ▼
                                       ┌──────────────────────────┐
                                       │  workload runners/       │
                                       │   transcode-runner       │
                                       │   abr-runner             │
                                       └──────────────────────────┘
```

## Tech stack lock

Canonical lock: Fastify 5 + Zod 3 + drizzle-orm + ESM TS 5 + Node 20+
+ pnpm + Postgres 16 + Redis 7 + Lit 3. License MIT.

Variance: customer-side RTMP listener uses a TS RTMP library (decided
pure-TS per plan §14 Q1; broker uses Go + yutopp/go-rtmp as locked in
plan 0011-followup §4.5). Not a stack variance — a library choice.

## Schema namespaces

- `app.*` — owned by customer-portal (customers, api_keys, reservations,
  topups, audit, idempotency).
- `media.*` — owned by video-gateway (assets, encoding_jobs,
  live_streams, playback_ids, renditions, uploads, pricing_live,
  pricing_vod, usage_records, webhook_endpoints, webhook_deliveries,
  live_session_debits, projects, recordings).

The suite mixed both schemas in one migration; the rewrite splits
ownership (plan §14 Q3 lock).

## Locked decisions

See plan §14 for the 14 locked decisions (Q1–Q10 + OQ1–OQ4).
