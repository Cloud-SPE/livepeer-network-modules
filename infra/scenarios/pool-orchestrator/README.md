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
- A delegated settlement signing key for the broker, minted with
  `livepeer-capability-broker settlement-key generate`. This is a hot broker
  key, not the cold orchestrator key. Its public half must be delegated by the
  next cold-signed manifest.
- A 32-byte broker sealing key, generated once and backed up securely. It
  protects attach credentials and private session state at rest.
- A public HTTPS broker origin. Workload authorizations bind this route, so it
  must be the same origin gateways resolve from the signed manifest.

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

`pool-controller` no longer renders broker config. Static trust, route, and
storage configuration belongs to the broker operator; the controller pushes
only mutable offers and attach credential hashes. The production path is:

1. copy `.env.example` to `.env`, set the required values, generate the
   settlement and sealing keys, and run `./up.sh`. It atomically renders the
   operator-owned bootstrap config, validates it with the broker image, checks
   the settlement key, validates Compose, and only then starts containers
2. bootstrap `pool-controller` with `template_catalog_dir` pointing at the
   workload catalog (repo-root `templates/`)
3. enable the templates this pool sells and price them
   (`PUT /admin/v1/template-overrides/{id}`). The offer set is *derived* from
   the enabled ones — there is no separate offer catalog to author
4. members sign in with their wallet and enrol a host, then run the bundle,
   which contains the agent and nothing else. There is no join request and no
   approval step — the pool never dials a member endpoint, so there is nothing
   to verify before admission
5. placement policy matches each reported GPU to enabled templates by
   `requirements` + `priority` + `stacking`; review
   `GET /admin/v1/placement-plan` and commit it with
   `POST /admin/v1/placement-plan/apply`
6. the agent pulls its desired state and starts the runners, then re-attaches
   declaring them; the broker certifies each and freezes the offer's
   runner-declared shape
7. the ladder promotes a passing placement from `probationary` to `active` on
   its own, once a settlement round has closed and it has completed the
   template's `min_jobs`

The broker image runs as uid `65532`. Keep both private key files mode `0400`
and owned by `65532:65532`; preflight tests readability as that container user.
The rendered YAML is mode `0644` because it contains paths and secret
references, not secret values.

The rendered bootstrap contains `offers_source: admin` and `offers: []`.
The controller pushes derived offers and credentials to the broker over the
admin API whenever pool state changes (plan 0043); runner facts come from the
runners. It never receives the settlement private key and cannot change the
broker's route identity or persistent-store paths.

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

cd infra/scenarios/pool-orchestrator
./up.sh
```

To validate without starting the stack, render and run preflight explicitly:

```bash
mkdir -p run
umask 077
./render-broker-config.sh > run/broker-host-config.yaml
./preflight.sh
```

Preflight is intentionally production-strict. Missing key material, an
invalid orchestrator address, a non-HTTPS public origin, an invalid broker
config, or an invalid Compose model stops before container creation. The
broker itself still permits unsigned mock configurations for development.

The Docker-first regression test builds the current broker, validates a
rendered bootstrap and delegated key as the runtime uid, checks Compose, and
proves that a missing key fails closed:

```bash
./test.sh
```

## Notes

- This scenario is suitable for a single-host public/data-plane rollout.
- Put TLS in front of the exposed broker and coordinator public ports before
  live traffic.
- Treat this scenario README as the compose/bootstrap guide only. The
  production control-plane and broker-apply workflow now lives in
  [`pool-controller/RUNBOOK.md`](../../../pool-controller/RUNBOOK.md).
- The operator-owned broker bootstrap carries `receipt_sink` and
  `pool_snapshot`, so broker work receipts flow to the controller and its
  selection state gates Pool routing. These are static connections; the
  controller does not render them.
- The compose file exposes broker worker QUIC on UDP
  `${BROKER_WORKER_QUIC_PORT:-8443}`. The bootstrap renders
  `listen.attach_quic: ":8443"`; set
  `bootstrap.public_broker_quic_addr: "<public-host>:8443"` in the controller
  config so member bundles advertise the same endpoint.
