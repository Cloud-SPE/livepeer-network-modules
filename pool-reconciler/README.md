# pool-reconciler

`pool-reconciler` is the round-close producer boundary for Pool accounting.

Operator runbook:
- [`RUNBOOK.md`](./RUNBOOK.md)

Current scope:

- load reconciler config,
- query round timing from `protocol-daemon` over its unix-socket gRPC surface,
- query confirmed round revenue from `payment-daemon`,
- query final work receipts from `pool-controller`,
- load a round-close request from JSON,
- submit it to `pool-controller /admin/v1/round-close`,
- persist durable round-close attempt state in BoltDB,
- bounded startup backfill for missed rounds while the reconciler was down,
- keep the producer boundary separate from both `protocol-daemon` and
  `pool-controller`.

The first implementation started manual/file-driven. It now also includes a
protocol-daemon-triggered loop with local retry/checkpoint state.

## Current commands

```bash
make build
make test
```

Compose entrypoint:

```bash
docker compose -f compose/docker-compose.yml up -d
```

Round-source inspection:

```bash
./bin/livepeer-pool-reconciler close-round \
  --config examples/pool-reconciler-config.example.yaml

./bin/livepeer-pool-reconciler watch-rounds \
  --config examples/pool-reconciler-config.example.yaml

./bin/livepeer-pool-reconciler prepare-round-close \
  --config examples/pool-reconciler-config.example.yaml

./bin/livepeer-pool-reconciler prepare-round-close \
  --config examples/pool-reconciler-config.example.yaml \
  --output /tmp/round-close.json

./bin/livepeer-pool-reconciler prepare-round-close \
  --config examples/pool-reconciler-config.example.yaml \
  --round-id 124

./bin/livepeer-pool-reconciler get-round-revenue \
  --config examples/pool-reconciler-config.example.yaml \
  --round-id 124

./bin/livepeer-pool-reconciler get-round-status \
  --config examples/pool-reconciler-config.example.yaml

./bin/livepeer-pool-reconciler stream-round-events \
  --config examples/pool-reconciler-config.example.yaml
```

Manual submit:

```bash
docker run --rm \
  -v "$PWD/examples/pool-reconciler-config.example.yaml:/etc/livepeer/pool-reconciler.yaml:ro" \
  -v "$PWD/examples/round-close-request.example.json:/work/round-close.json:ro" \
  tztcloud/livepeer-pool-reconciler:dev \
  submit-round-close \
  --config /etc/livepeer/pool-reconciler.yaml \
  --request /work/round-close.json
```

When `round_source.protocol_daemon_socket` is configured, `submit-round-close`
refuses to close the current or a future round. The request's `round_id` must
be numerically less than the latest round observed by `protocol-daemon`.

When `payment_daemon.socket` is configured, `prepare-round-close` pre-fills
`pool_revenue_wei` from confirmed round revenue and computes `pool_cut_wei`
from `pool.commission_bps`. It still leaves `included_work_receipt_ids` empty
unless matching final work receipts already exist in `pool-controller` for that
explicit `round_id`.

`close-round` reuses the same preparation path and immediately submits the
derived payload to `pool-controller`. It records an attempt/closed/failed
state in the local BoltDB file at `reconcile.state_path`.

`watch-rounds` consumes `protocol-daemon` round events and attempts to close
the just-finished round on each transition.

Before it starts streaming, `watch-rounds` performs a bounded startup
backfill over the most recent completed rounds up to
`reconcile.backfill_limit`. Rounds already marked `closed` in the local state
store are skipped; failed rounds are retried on the next startup or live
round transition.

While the watcher is running, a retry ticker driven by
`reconcile.retry_interval_ms` re-attempts pending failed rounds from the local
state store without waiting for the next round transition.
