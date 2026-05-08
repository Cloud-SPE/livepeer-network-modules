# vtuber-gateway

Reference gateway-host deployment for the vtuber product:

- `vtuber-gateway`
- `payment-daemon` in sender mode
- `postgres`

This scenario assumes the vtuber worker path is already reachable at an
external broker URL. `vtuber-gateway` currently uses a static broker URL.

## Bring-up

```sh
cp infra/scenarios/vtuber-gateway/.env.example infra/scenarios/vtuber-gateway/.env
docker compose \
  -f infra/scenarios/vtuber-gateway/docker-compose.yml \
  --env-file infra/scenarios/vtuber-gateway/.env \
  up -d
```

## Files you must provide

- `./run/gateway-hot-wallet-keystore.json`
- `./run/keystore-password`

## Verify

```sh
curl -s http://127.0.0.1:3001/healthz
```

If the health check is green, the gateway booted with its database and
payer socket. Actual session-open traffic still depends on the external
worker broker being reachable at `LIVEPEER_BROKER_URL`.
