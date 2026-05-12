# video-gateway

Reference gateway-host deployment for the video product:

- `video-gateway`
- `payment-daemon` in sender mode
- `postgres`
- `redis`
- `rustfs`

This scenario assumes the media worker path is already reachable at an
external broker URL. `video-gateway` currently uses a static broker URL
here, but resolver/manifest mode is the preferred production shape.
This static scenario is mainly for simple bring-up and debugging.

## Bring-up

```sh
cp infra/scenarios/archive/video-gateway/.env.example infra/scenarios/archive/video-gateway/.env
docker compose \
  -f infra/scenarios/archive/video-gateway/docker-compose.yml \
  --env-file infra/scenarios/archive/video-gateway/.env \
  up -d
```

## Files you must provide

- `./run/gateway-hot-wallet-keystore.json`
- `./run/keystore-password`

## Verify

```sh
curl -s http://127.0.0.1:3000/healthz
redis-cli -u redis://127.0.0.1:6379/0 ping
```

If the health check is green, the gateway booted with its database,
redis, storage origin, and payer socket. Actual live/VOD traffic still
depends on the external worker broker being reachable at
`LIVEPEER_BROKER_URL`.

The active VOD path is `video:transcode.abr` for all offline video
work, including one-rendition jobs.
