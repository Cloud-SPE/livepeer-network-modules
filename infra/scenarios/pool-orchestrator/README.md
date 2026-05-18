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

## Prepare broker runtime

`pool-controller` no longer renders broker config from a nested controller YAML.
The production path is:

1. bootstrap `pool-controller`
2. create offers through the control-plane
3. approve members and backends
4. create assignments
5. use `POST /admin/v1/broker-runtime/apply`

The controller now derives desired broker runtime from persisted state and
confirms convergence against broker-reported runtime revision and attempt ID.

See:

- [`pool-controller/RUNBOOK.md`](../../pool-controller/RUNBOOK.md)
- [`docs/design-docs/pool-orchestrator-production-rollout.md`](../../docs/design-docs/pool-orchestrator-production-rollout.md)

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
- Treat this scenario README as the compose/bootstrap guide only. The
  production control-plane and broker-apply workflow now lives in
  [`pool-controller/RUNBOOK.md`](../../../pool-controller/RUNBOOK.md).
- The broker runtime artifact applied by `pool-controller` carries
  `receipt_sink`, so broker-side work receipts flow back into
  `pool-controller` when configured in controller state/bootstrap config.
