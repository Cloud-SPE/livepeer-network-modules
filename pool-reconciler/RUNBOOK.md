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

## Metrics

When `reconcile.metrics_addr` is set (e.g. `:9091`), `watch-rounds` exposes
Prometheus metrics on `/metrics`:

- `livepeer_pool_reconciler_round_close_total{outcome}` — counter, outcome is
  `closed` or `failed`. The ratio is the leading signal of reconciler health.
- `livepeer_pool_reconciler_round_close_duration_seconds{outcome}` — histogram
  of wall-clock time spent in `attemptRoundClose`.
- `livepeer_pool_reconciler_pending_rounds_retried_total` — counter, ticks once
  per pending round the retry ticker touches.

If `reconcile.metrics_addr` is empty, the listener is not started.

## Detecting reconciler failure pressure

| Symptom on dashboard | Likely cause | First step |
|---|---|---|
| `round_close_total{outcome="failed"}` rising without `outcome="closed"` rising | Validation or controller submission is failing for every round | Inspect the `error` field on `retry_tick` / `round_event` log entries; common causes are stale `payment-daemon` revenue or a misconfigured `commission_bps` |
| `round_close_total{outcome="failed"}` rising with `pending_rounds_retried_total` rising in lockstep | Same round retrying forever | Check the local state — `GetRound(roundID).LastError` shows the most recent failure reason. If the underlying RPC source is healthy, manually `close-round` after fixing the input |
| `round_close_duration_seconds` p99 > a few seconds | `pool-controller` slow to acknowledge, or `payment-daemon` revenue lookup slow | Inspect controller and payment-daemon latency before suspecting the reconciler |

## Recovery notes

- Persist `reconcile.state_path`; otherwise, the reconciler loses checkpoint
  and retry context across restarts.
- If the reconciler is down, accounting progression stops, but request serving
  is unaffected.
- If a round repeatedly fails with the same error after the retry ticker has
  exhausted its window, treat the local state as authoritative: stop the
  reconciler, back up `reconcile.state_path`, fix the upstream cause
  (commonly: protocol-daemon round source, payment-daemon revenue snapshot, or
  controller-side accounting), then resume. The retry ticker will pick up
  the still-pending round automatically.

## Backup scope

Back up the BoltDB file at `reconcile.state_path`. It is not accounting truth,
but it is the job checkpoint used for backfill and retry behavior.
