# pool-reconciler runbook

## Role

`pool-reconciler` is the round-close producer. It consumes:

- round timing from `protocol-daemon`
- confirmed round revenue from `payment-daemon`
- final work receipts from `pool-controller`

It then submits canonical round-close payloads back to `pool-controller`.

## Required runtime inputs

- reconciler config YAML
- durable `reconcile.state_path`
- reachable:
  - `protocol-daemon`
  - `payment-daemon`
  - `pool-controller`

## Start

One-shot close:

```bash
go run ./cmd/livepeer-pool-reconciler close-round \
  --config ./examples/pool-reconciler-config.example.yaml
```

Long-running watcher:

```bash
go run ./cmd/livepeer-pool-reconciler watch-rounds \
  --config ./examples/pool-reconciler-config.example.yaml
```

Compose:

```bash
docker compose -f compose/docker-compose.yml up -d
```

## Operational expectations

- On startup, `watch-rounds` backfills a bounded number of missed completed
  rounds.
- Closed rounds are checkpointed locally and not re-closed.
- Failed rounds are retried according to the reconciler retry ticker.

## Recovery notes

- Persist `reconcile.state_path`; otherwise, the reconciler loses checkpoint
  and retry context across restarts.
- If the reconciler is down, accounting progression stops, but request serving
  is unaffected.

## Backup scope

Back up the BoltDB file at `reconcile.state_path`. It is not accounting truth,
but it is the job-runner checkpoint used for backfill and retry behavior.
