# pool-payout-executor

`pool-payout-executor` is the future payout-submission worker for Pool member
distribution.

Operator runbook:
- [`RUNBOOK.md`](./RUNBOOK.md)

Current scope:

- load executor config,
- read payout intents from `pool-controller`,
- filter and batch exported intents for downstream processing,
- write status updates back to `pool-controller`,
- submit native `ETH` payouts on Arbitrum,
- confirm submitted transfers back into `paid` or `failed`.

This first implementation uses native `ETH` as the payout rail. It signs
transfers from a dedicated hot wallet and writes back `submitted`, `paid`, or
`failed` state into `pool-controller`. The preferred signer input is a local
geth-compatible `keystore.json` plus password file configured in the executor
YAML.

A live Arbitrum dust payout has been validated end to end against this path.

`send-native-batch` supports `--dry-run` for operator preview and refuses to
rebroadcast intents that already carry `external_ref` or `tx_hash` settlement
metadata.

When broadcasting a real exported batch, the executor now claims those intents
from `pool-controller` with a bounded lease before submitting them. That lease
prevents another executor instance from picking the same exported payouts until
the lease is consumed or expires.

On restart, the executor first looks for still-valid leased intents already
owned by its configured `executor_id` and resumes those before claiming a fresh
exported batch.

If a claimed batch turns out to be unsendable before any submission starts,
the executor now releases that lease back to `pool-controller` immediately
instead of waiting for TTL expiry.

If a leased batch is only partially sendable, the executor keeps attempted
members under the current lease/state and releases the untouched remainder back
to `exported`.

`reconcile-once` is the executor-friendly one-shot loop: it confirms current
`submitted` payouts first, then processes the next `exported` native-`ETH`
batch. With `--dry-run`, both phases become read-only previews.

`reconcile-loop` repeats that same flow on a fixed interval for unattended
executor operation. It uses the exact same confirmation and dispatch logic as
`reconcile-once`.

When `executor.auto_requeue_failed` is enabled, that same reconcile flow also
inspects `failed` intents and requeues only those that:
- are still below `executor.max_retries`
- have been failed for at least `executor.requeue_cooldown_seconds`
- look transient based on their persisted failure reason

This policy is disabled by default.

When `executor.state_path` is set, both reconcile commands persist local BoltDB
state for recent run history and per-intent retry metadata. `state-summary`
reads that state back for operator inspection.

When backoff is configured, reconcile commands use that local retry metadata to
temporarily skip repeatedly failing dispatch attempts or repeated failed/pending
confirm checks. Direct `send-native-batch` and `confirm-submitted` remain
manual override surfaces and do not apply local backoff.

## Current commands

```bash
make test
```

Docker-first gestures:

```bash
make build
make test
make run
```

Compose entrypoint:

```bash
docker compose -f compose/docker-compose.yml up -d
```

Fetch exported intents:

```bash
go run ./cmd/livepeer-pool-payout-executor list-intents \
  --config examples/pool-payout-executor-config.example.yaml \
  --status exported \
  --limit 25
```

Prepare a batch payload:

```bash
go run ./cmd/livepeer-pool-payout-executor prepare-batch \
  --config examples/pool-payout-executor-config.example.yaml \
  --status exported \
  --limit 25
```

Send a native-ETH batch:

```bash
go run ./cmd/livepeer-pool-payout-executor send-native-batch \
  --config examples/pool-payout-executor-config.example.yaml \
  --status exported \
  --limit 25
```

Preview a native-ETH batch without broadcasting:

```bash
go run ./cmd/livepeer-pool-payout-executor send-native-batch \
  --config examples/pool-payout-executor-config.example.yaml \
  --status exported \
  --limit 25 \
  --dry-run
```

Confirm submitted transfers:

```bash
go run ./cmd/livepeer-pool-payout-executor confirm-submitted \
  --config examples/pool-payout-executor-config.example.yaml \
  --status submitted \
  --limit 25
```

Inspect controller payout alerts through executor auth/config:

```bash
go run ./cmd/livepeer-pool-payout-executor list-alerts \
  --config examples/pool-payout-executor-config.example.yaml \
  --submitted-older-than-seconds 900 \
  --retry-count-at-least 3
```

Requeue failed intents back to `exported`:

```bash
go run ./cmd/livepeer-pool-payout-executor requeue-failed \
  --config examples/pool-payout-executor-config.example.yaml \
  --status failed \
  --limit 25
```

Requeue only controller-flagged stale failed intents:

```bash
go run ./cmd/livepeer-pool-payout-executor requeue-alerted-failed \
  --config examples/pool-payout-executor-config.example.yaml \
  --failed-older-than-seconds 3600
```

Run one reconciliation pass:

```bash
go run ./cmd/livepeer-pool-payout-executor reconcile-once \
  --config examples/pool-payout-executor-config.example.yaml \
  --limit 25
```

Preview one reconciliation pass without writing status or broadcasting:

```bash
go run ./cmd/livepeer-pool-payout-executor reconcile-once \
  --config examples/pool-payout-executor-config.example.yaml \
  --limit 25 \
  --dry-run
```

Run the same flow continuously:

```bash
go run ./cmd/livepeer-pool-payout-executor reconcile-loop \
  --config examples/pool-payout-executor-config.example.yaml \
  --limit 25 \
  --interval-ms 5000
```

Inspect local executor state:

```bash
go run ./cmd/livepeer-pool-payout-executor state-summary \
  --config examples/pool-payout-executor-config.example.yaml \
  --runs-limit 10 \
  --intents-limit 25
```

Write executor status back:

```bash
go run ./cmd/livepeer-pool-payout-executor mark-submitted \
  --config examples/pool-payout-executor-config.example.yaml \
  --ids payout-124-0xabc,payout-124-0xdef \
  --external-ref batch-17 \
  --tx-hash 0xabc123
```

```bash
go run ./cmd/livepeer-pool-payout-executor mark-paid \
  --config examples/pool-payout-executor-config.example.yaml \
  --ids payout-124-0xabc \
  --tx-hash 0xdef456
```

```bash
go run ./cmd/livepeer-pool-payout-executor mark-failed \
  --config examples/pool-payout-executor-config.example.yaml \
  --ids payout-124-0xabc \
  --external-ref batch-18 \
  --reason rpc-timeout
```

`requeue-failed` remains the explicit manual override surface. Unattended
auto-requeue is available now, but it is disabled by default and intentionally
conservative: `max_retries=3`, `requeue_cooldown_seconds=3600`, and only
transient-looking failures are eligible.

For local live-chain testing, keep these files side by side:

- `pool-payout-executor-config.local.yaml`
- `keystore.json`
- `keystore-password`

`keystore_path` and `keystore_password_path` resolve relative to the config
file path when you use `--config ...`.
