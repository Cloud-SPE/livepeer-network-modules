# openai-gateway manifest mode

Reference gateway-side stack for manifest-driven routing:

- `openai-gateway`
- `service-registry-daemon` in resolver mode
- `payment-daemon` in sender mode
- `postgres`

The gateway resolves active orchestrators from chain, fetches each
signed manifest from the on-chain `serviceURI`, selects a route, and
sends paid requests directly to the chosen broker.

## Bring-up

```sh
cp infra/scenarios/openai-gateway-manifest/.env.example infra/scenarios/openai-gateway-manifest/.env
docker compose \
  -f infra/scenarios/openai-gateway-manifest/docker-compose.yml \
  --env-file infra/scenarios/openai-gateway-manifest/.env \
  up -d
```

## Files you must provide

- `./run/gateway-hot-wallet-keystore.json`
- `./run/keystore-password`

## Verify

```sh
curl -s http://127.0.0.1:3000/healthz
curl -s http://127.0.0.1:9095/metrics | head
docker logs $(docker compose -f infra/scenarios/openai-gateway-manifest/docker-compose.yml ps -q service-registry-daemon) --tail 50
```
