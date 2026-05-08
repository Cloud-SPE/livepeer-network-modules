# secure-orch control plane

Reference stack for the rewrite control plane:

- `orch-coordinator` publishes the signed manifest
- `protocol-daemon` handles round-init, reward, and on-chain URI writes
- `service-registry-daemon` resolves active orchestrators from chain
- `secure-orch-console` signs candidates with the cold key

This compose can run co-located for reference and smoke usage, but the
logical production split is:

- public USEast host: `orch-coordinator`
- cold-key host: `secure-orch-console`
- chain-side operator host: `protocol-daemon`
- consumer/gateway host or shared control-plane host: `service-registry-daemon`

## Bring-up

```sh
cp infra/scenarios/secure-orch-control-plane/.env.example infra/scenarios/secure-orch-control-plane/.env
cp infra/scenarios/secure-orch-control-plane/orch-coordinator.yaml ./orch-coordinator.yaml
docker compose \
  -f infra/scenarios/secure-orch-control-plane/docker-compose.yml \
  --env-file infra/scenarios/secure-orch-control-plane/.env \
  up -d
```

## Files you must provide

- `./run/protocol-keystore.json`
- `./run/protocol-keystore-password`
- `./run/secure-orch-keystore.json`
- `./run/secure-orch-password`

## Verify

```sh
curl -s http://127.0.0.1:9091/healthz
curl -s http://127.0.0.1:9095/metrics | head
curl -s http://127.0.0.1:8080/candidate.json
curl -s http://127.0.0.1:8081/.well-known/livepeer-registry.json
```

`/.well-known/livepeer-registry.json` returns `no manifest published`
until you sign and upload the first manifest.
