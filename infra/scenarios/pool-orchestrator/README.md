# Pool Orchestrator Scenario

This scenario is the **public/data-plane side** of a Pool-based orchestrator.
It runs on the host that serves the broker, coordinator, and Pool accounting
workers.

It includes:

- `pool-controller`
- `capability-broker`
- `payment-daemon-receiver`
- `orch-coordinator`
- `pool-reconciler`
- `pool-payout-executor`

It does **not** include the cold/protocol host. You still need the separate
secure-orch side to run:

- `protocol-daemon`
- `service-registry-daemon`
- `secure-orch-console`

## Prerequisites

- A separate secure-orch / protocol host already running `protocol-daemon`
  and exposing `/var/run/livepeer/protocol.sock` on this host.
- A funded payment-daemon receiver wallet:
  - `PAYMENT_KEYSTORE`
  - `PAYMENT_KEYSTORE_PASSWORD_FILE`
- A funded payout hot wallet:
  - `POOL_PAYOUT_EXECUTOR_KEYSTORE`
  - `POOL_PAYOUT_EXECUTOR_KEYSTORE_PASSWORD_FILE`
- A real coordinator config at `./coordinator-config.yaml`
- A generated broker host-config at `./run/generated-broker-host-config.yaml`

## Build images

From the repo root:

```bash
./infra/scripts/build-images.sh \
  livepeer-pool-controller \
  livepeer-pool-reconciler \
  livepeer-pool-payout-executor \
  livepeer-capability-broker \
  livepeer-payment-daemon \
  livepeer-orch-coordinator
```

## Render broker config

`pool-controller` remains the source-of-truth config. Render the broker
`host-config.yaml` from it before bringing up the stack:

```bash
mkdir -p infra/scenarios/pool-orchestrator/run
docker run --rm \
  -e POOL_CONTROLLER_ADMIN_TOKEN="${POOL_CONTROLLER_ADMIN_TOKEN}" \
  -v "$PWD/pool-controller/examples/pool-controller-config.compose.yaml:/etc/livepeer/pool-controller.yaml:ro" \
  -v "$PWD/infra/scenarios/pool-orchestrator/run:/out" \
  tztcloud/livepeer-pool-controller:v1.1.0 \
  generate-broker-config \
  --config /etc/livepeer/pool-controller.yaml \
  > infra/scenarios/pool-orchestrator/run/generated-broker-host-config.yaml
```

## Configure coordinator

Copy and edit:

```bash
cp infra/scenarios/pool-orchestrator/coordinator-config.example.yaml \
   infra/scenarios/pool-orchestrator/coordinator-config.yaml
```

Set:

- `identity.orch_eth_address`
- `brokers[0].base_url` to the public TLS URL for this Pool broker

## Bring up

```bash
cp infra/scenarios/pool-orchestrator/.env.example \
   infra/scenarios/pool-orchestrator/.env
$EDITOR infra/scenarios/pool-orchestrator/.env

docker compose \
  -f infra/scenarios/pool-orchestrator/docker-compose.yml \
  --env-file infra/scenarios/pool-orchestrator/.env \
  up -d
```

## Notes

- This scenario is suitable for a single-host public/data-plane rollout.
- Put TLS in front of the exposed broker and coordinator public ports before
  live traffic.
- The generated broker config now carries `receipt_sink`, so broker-side work
  receipts flow back into `pool-controller` when configured in the
  pool-controller source config.
