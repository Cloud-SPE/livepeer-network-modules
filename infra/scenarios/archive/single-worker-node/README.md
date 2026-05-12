# single worker node

Reference worker-node stack for one brokered capability:

- `capability-broker`
- `payment-daemon` in receiver mode
- one local `vllm_model_runner`

This is the worker-side deployment shape the coordinator scrapes and
gateways eventually call.

## Bring-up

```sh
cp infra/scenarios/archive/single-worker-node/.env.example infra/scenarios/archive/single-worker-node/.env
docker compose \
  -f infra/scenarios/archive/single-worker-node/docker-compose.yml \
  --env-file infra/scenarios/archive/single-worker-node/.env \
  up -d
```

## Files you must provide

- `./run/hot-wallet-keystore.json`
- `./run/keystore-password`
- `./host-config.yaml` is already provided in this scenario directory;
  edit the orch address, label, and price before use

## Verify

```sh
curl -s http://127.0.0.1:8080/registry/offerings | jq
curl -s http://127.0.0.1:8080/registry/health | jq
curl -s http://127.0.0.1:9090/metrics | head
```
