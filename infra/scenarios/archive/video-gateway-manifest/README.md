# video-gateway manifest mode

Reference gateway-side stack for manifest-driven routing:

- `video-gateway`
- `service-registry-daemon` in resolver mode
- `payment-daemon` in sender mode
- `postgres`
- `redis`
- `rustfs`

The gateway resolves active orchestrators from chain, fetches each
signed manifest from the on-chain `serviceURI`, selects a route for VOD
transcode requests, and sends paid requests directly to the chosen
broker. The active VOD capability model is `video:transcode.abr` for
all offline video work, including one-rendition jobs. The
`video-gateway` image now bakes in the customer-portal
migrations and resolver proto contracts, so this scenario does not need
extra bind mounts for those assets.

`video-gateway` still has a separate `LIVEPEER_BROKER_RTMP_HOST` knob
for customer-side RTMP proxying. For VOD-only deployments that value is
not on the critical path; for live RTMP, point it at the broker host
that should receive proxied ingest traffic.

## Bring-up

```sh
cp infra/scenarios/archive/video-gateway-manifest/.env.example infra/scenarios/archive/video-gateway-manifest/.env
docker compose \
  -f infra/scenarios/archive/video-gateway-manifest/docker-compose.yml \
  --env-file infra/scenarios/archive/video-gateway-manifest/.env \
  up -d
```

## Files you must provide

- `./run/gateway-hot-wallet-keystore.json`
- `./run/keystore-password`

## Verify

```sh
curl -s http://127.0.0.1:3000/healthz
redis-cli -u redis://127.0.0.1:6379/0 ping
curl -s http://127.0.0.1:9095/metrics | head
docker logs $(docker compose -f infra/scenarios/archive/video-gateway-manifest/docker-compose.yml ps -q service-registry-daemon) --tail 50
```

Then verify the operator surfaces:

```sh
curl -s http://127.0.0.1:3000/admin/video/resolver-candidates \
  -H "Authorization: Bearer $VIDEO_GATEWAY_ADMIN_TOKEN" \
  -H "X-Actor: ops"
curl -s http://127.0.0.1:3000/admin/playback \
  -H "Authorization: Bearer $VIDEO_GATEWAY_ADMIN_TOKEN" \
  -H "X-Actor: ops"
```
