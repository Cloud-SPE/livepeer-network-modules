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
- `GET /metrics` on `listen.metrics` (default `:9090`)

## High-value admin reads

- `GET /admin/v1/state`
- `GET /admin/v1/snapshots`
- `GET /admin/v1/payout-intents`
- `GET /admin/v1/member-payouts`
- `GET /admin/v1/payout-rounds`
- `GET /admin/v1/payout-alerts`

## Metrics

Scrape the dedicated metrics listener, not the admin port.

High-value Pool routing metrics now include:

- backend selection counts by `state`
- grouped `routing_reason` and `exclusion_reason` counts
- automatic warm-up and cooldown counts
- average effective selection score by offering
- average recent-window age by offering
- the live scorer settings currently applied after defaults and reload
- backend outcome ingest counts by outcome class
- synthetic probe run totals and durations
- per-capability synthetic probe result counts by `status` and `reason`
- persisted work-receipt counts by `status`
- persisted payout-intent counts by `status`
- receipt-write action counters across `work` and `round` flows
- payout-intent action counters across derive/export/claim/renew/release/requeue/status updates

## Recovery notes

- `pool-controller` restarts are safe if `--data-dir` is persisted.
- If `pool-controller` is down, previously loaded broker config remains in the
  broker process; traffic can continue.
- Do not delete the BoltDB state unless you intentionally want to discard
  payout and receipt history.

## Backup scope

Back up the entire `--data-dir`. That store contains the canonical Pool-side
receipt and payout accounting history.
