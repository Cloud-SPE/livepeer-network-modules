# full minimal network

Reference end-to-end topology that ties together:

- secure-orch control plane
- one worker node
- one OpenAI gateway

This is intentionally the smallest stack that demonstrates how the
rewrite modules fit together in one environment. The OpenAI gateway uses
the worker broker directly via a static URL so the stack is usable
before the first signed manifest is published on chain.

Included services:

- `orch-coordinator`
- `protocol-daemon`
- `service-registry-daemon`
- `secure-orch-console`
- `capability-broker`
- `payment-daemon` receiver
- `vllm_model_runner`
- `openai-gateway`
- `payment-daemon` sender
- `postgres`

## Bring-up

```sh
cp infra/scenarios/full-minimal-network/.env.example infra/scenarios/full-minimal-network/.env
docker compose \
  -f infra/scenarios/full-minimal-network/docker-compose.yml \
  --env-file infra/scenarios/full-minimal-network/.env \
  up -d
```

## Files you must provide

- `./run/protocol-keystore.json`
- `./run/protocol-keystore-password`
- `./run/secure-orch-keystore.json`
- `./run/secure-orch-password`
- `./run/worker-hot-wallet-keystore.json`
- `./run/gateway-hot-wallet-keystore.json`
- `./run/keystore-password`

## Suggested verification order

```sh
curl -s http://127.0.0.1:8080/registry/offerings | jq
curl -s http://127.0.0.1:9091/healthz
curl -s http://127.0.0.1:8088/candidate.json | jq
curl -s http://127.0.0.1:3000/healthz
```

Once the worker and coordinator are healthy:

1. download `candidate.json` from the coordinator
2. sign it in `secure-orch-console`
3. upload the signed manifest back to the coordinator
4. then use `protocol-daemon` to set the public coordinator URL on chain
