# pool-controller runbook

## Role

`pool-controller` is the Pool accounting and admin source of truth. It is not
in the request path, but it is the source of record for:

- member config snapshots
- work receipts
- round receipts
- payout intents
- payout retry history
- lease state

## Required runtime inputs

- a Pool config YAML
- a durable `--data-dir`
- admin bearer auth:
  - `admin_auth.bearer_token_ref: env://...` in production
  - `admin_auth.bearer_token` is acceptable only for local testing

## Start

```bash
go run ./cmd/livepeer-pool-controller serve \
  --config ./examples/pool-controller-config.example.yaml \
  --data-dir ./var/pool-controller \
  --listen :8080
```

Compose:

```bash
docker compose -f compose/docker-compose.yml up -d
```

## Health checks

- `GET /healthz`
- `GET /readyz`

## High-value admin reads

- `GET /admin/v1/state`
- `GET /admin/v1/snapshots`
- `GET /admin/v1/payout-intents`
- `GET /admin/v1/member-payouts`
- `GET /admin/v1/payout-rounds`
- `GET /admin/v1/payout-alerts`

## Recovery notes

- `pool-controller` restarts are safe if `--data-dir` is persisted.
- If `pool-controller` is down, previously loaded broker config remains in the
  broker process; traffic can continue.
- Do not delete the BoltDB state unless you intentionally want to discard
  payout and receipt history.

## Backup scope

Back up the entire `--data-dir`. That store contains the canonical Pool-side
receipt and payout accounting history.
