# Video Gateway Operator Runbook

This runbook describes the current `video-gateway` deployment and
recovery shape as it actually ships in this repo.

## Deployment modes

Two reference scenarios exist:

- static broker mode:
  [`infra/scenarios/archive/video-gateway/README.md`](../../infra/scenarios/archive/video-gateway/README.md)
- resolver/manifest mode:
  [`infra/scenarios/archive/video-gateway-manifest/README.md`](../../infra/scenarios/archive/video-gateway-manifest/README.md)

For the current product model, resolver mode is the preferred path for
VOD and route selection.

## Required services

`video-gateway` expects:

- Postgres
- Redis
- S3-compatible storage
- sender `payment-daemon`
- `service-registry-daemon` in resolver mode for manifest routing
- an external broker and media workers advertising:
  - `video:transcode.abr`
  - `video:live.rtmp`

## Required runtime env

Minimum gateway wiring:

- `DATABASE_URL`
- `REDIS_URL`
- `CHAIN_RPC`
- `LIVEPEER_RESOLVER_SOCKET`
- `LIVEPEER_PAYER_DAEMON_SOCKET`
- `CUSTOMER_PORTAL_PEPPER`
- `VIDEO_WEBHOOK_HMAC_PEPPER`
- `VIDEO_GATEWAY_ADMIN_TOKENS`
- `S3_ENDPOINT`
- `S3_BUCKET`
- `S3_ACCESS_KEY_ID`
- `S3_SECRET_ACCESS_KEY`
- `S3_REGION`

## Current active product model

- all VOD work routes through `video:transcode.abr`
- live ingest routes through `video:live.rtmp`
- project ownership is first-class across assets, streams, recordings,
  webhooks, and playback
- broker capability identity and pricing live in `capability-broker`
  host config, not runner-local `offering.yaml`

## Health checks

Basic gateway checks:

```bash
curl -sSf http://127.0.0.1:3000/healthz
curl -sSf http://127.0.0.1:3000/admin/video/resolver-candidates \
  -H "Authorization: Bearer $VIDEO_GATEWAY_ADMIN_TOKEN" \
  -H "X-Actor: ops"
```

Storage check:

```bash
docker compose logs rustfs --tail 50
```

Resolver check:

```bash
docker compose logs service-registry-daemon --tail 50
```

## Operator workflows

The admin console currently supports:

- node inspection and broker suppression
- asset retry/requeue
- recording retry/requeue
- live stream force-end
- playback policy inspection and mutation
- webhook failure replay

Primary URLs:

- admin: `/admin/console/`
- portal: `/portal/`

## Common recovery actions

### ABR worker returns failures

1. Open admin Assets.
2. Inspect job and rendition failure state.
3. Retry the asset if the failure is transient.
4. If a broker is misbehaving, suppress it from the Nodes view.

### Recording handoff failed

1. Open admin Recordings.
2. Confirm the failed recording has an `assetId`.
3. Retry the recording from admin.

### Bad broker route

1. Open admin Nodes.
2. Suppress the broker URL.
3. Confirm new jobs route elsewhere.
4. Unsuppress after worker recovery.

### Live session looks stuck

1. Open admin Streams.
2. Inspect `health`, `sessionKnown`, `brokerUrl`, and `idleSeconds`.
3. Force-end obviously stale sessions.

## Rebuild and redeploy

```bash
TAG=v1.0.0 REGISTRY=tztcloud ./infra/scripts/build-images.sh livepeer-video-gateway
docker compose up -d --force-recreate video-gateway
```

## Verification after deploy

Run:

```bash
pnpm -F @livepeer-network-modules/video-gateway test
pnpm -F @livepeer-network-modules/video-gateway-web-ui test
pnpm -F @livepeer-network-modules/video-gateway build
```

Then verify:

- admin login works and survives refresh
- portal login works and survives refresh
- `/admin/assets`, `/admin/streams`, `/admin/nodes`, `/admin/playback`
  load without server errors
- `/portal/assets`, `/portal/projects`, `/portal/webhooks` load without
  server errors
