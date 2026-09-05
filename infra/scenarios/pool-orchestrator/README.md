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
- `pool-member-agent` image build support for downloaded member bundles

It does **not** include the cold/protocol host. You still need the separate
secure-orch side to run:

- `protocol-daemon`
- `service-registry-daemon`
- `secure-orch-console`

## Prerequisites

- A separate secure-orch / protocol host already running `protocol-daemon`
  and exposing `/var/run/livepeer/protocol.sock` on this host.
- One or more Arbitrum RPC endpoints (`CHAIN_RPC_URLS`, comma-separated,
  primary first). The payout executor takes its list from the
  `executor.rpc_urls` key of its config file instead.
- A funded payment-daemon receiver wallet:
  - `PAYMENT_KEYSTORE_FILE`
  - `PAYMENT_KEYSTORE_PASSWORD_FILE`
- A funded payout hot wallet:
  - `POOL_PAYOUT_EXECUTOR_KEYSTORE_FILE`
  - `POOL_PAYOUT_EXECUTOR_KEYSTORE_PASSWORD_FILE`
- A real coordinator config at `./coordinator-config.yaml`
- A generated broker host-config at `./run/generated-broker-host-config.yaml`

## Build images

From the repo root:

```bash
./infra/scripts/build-images.sh \
  livepeer-pool-controller \
  livepeer-pool-member-agent \
  livepeer-pool-reconciler \
  livepeer-pool-payout-executor \
  livepeer-capability-broker \
  livepeer-payment-daemon \
  livepeer-orch-coordinator
```

## Prepare broker runtime

`pool-controller` no longer renders broker config from a nested controller YAML.
The production path is:

1. bootstrap `pool-controller` with `template_catalog_dir` pointing at the
   workload catalog (repo-root `templates/`)
2. enable the templates this pool sells and price them
   (`PUT /admin/v1/template-overrides/{id}`). The offer set is *derived* from
   the enabled ones — there is no separate offer catalog to author
3. members sign in with their wallet and enrol a host, then run the bundle,
   which contains the agent and nothing else. There is no join request and no
   approval step — the pool never dials a member endpoint, so there is nothing
   to verify before admission
4. placement policy matches each reported GPU to enabled templates by
   `requirements` + `priority` + `stacking`; review
   `GET /admin/v1/placement-plan` and commit it with
   `POST /admin/v1/placement-plan/apply`
5. the agent pulls its desired state and starts the runners, then re-attaches
   declaring them; the broker certifies each and freezes the offer's
   runner-declared shape
6. the ladder promotes a passing placement from `probationary` to `active` on
   its own, once a settlement round has closed and it has completed the
   template's `min_jobs`

> The five templates in `templates/` carry no `runner_compose` block yet — the
> v1 images and model ids are still open (`lnm-v12`) — so nothing will actually
> start on a member host from the shipped catalog. For an end-to-end scenario,
> add `runner_compose.image` to the template you enable.

The controller pushes its offers and credentials to the broker over the
admin API whenever pool state changes (plan 0043). There is no rendered
broker config and no apply step; runner facts come from the runners.

See:

- [`pool-controller/RUNBOOK.md`](../../../pool-controller/RUNBOOK.md)
- [`docs/design-docs/pool-orchestrator-production-rollout.md`](../../../docs/design-docs/pool-orchestrator-production-rollout.md)

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
- The compose file exposes broker worker QUIC on UDP
  `${BROKER_WORKER_QUIC_PORT:-8443}`. Set `listen.worker_quic: ":8443"` and
  `bootstrap.public_broker_quic_addr: "<public-host>:8443"` in the controller
  config to put QUIC into generated broker config and member bundles.
