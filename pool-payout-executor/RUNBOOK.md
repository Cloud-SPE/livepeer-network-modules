# pool-payout-executor runbook

## Role

`pool-payout-executor` is the native-`ETH` payout worker. It:

- reads exported or leased payout intents from `pool-controller`
- claims or renews leases
- signs Arbitrum transfers from a dedicated hot wallet
- writes `submitted`, `paid`, or `failed` state back to `pool-controller`

## Required runtime inputs

- executor config YAML
- Arbitrum RPC URL
- keystore file
- keystore password file
- durable `executor.state_path` if reconcile-loop persistence is desired

## Start

Preview a batch:

```bash
go run ./cmd/livepeer-pool-payout-executor send-native-batch \
  --config ./examples/pool-payout-executor-config.example.yaml \
  --dry-run
```

Send a real batch:

```bash
go run ./cmd/livepeer-pool-payout-executor send-native-batch \
  --config ./examples/pool-payout-executor-config.example.yaml
```

Confirm submitted payouts:

```bash
go run ./cmd/livepeer-pool-payout-executor confirm-submitted \
  --config ./examples/pool-payout-executor-config.example.yaml
```

Long-running loop:

```bash
go run ./cmd/livepeer-pool-payout-executor reconcile-loop \
  --config ./examples/pool-payout-executor-config.example.yaml \
  --interval-ms 5000
```

Compose:

```bash
docker compose -f compose/docker-compose.yml up -d
```

## Operational expectations

- Use a dedicated payout hot wallet.
- Keep `batch_size: 1` during initial rollout.
- Start from `--dry-run` before any new environment rollout.
- Watch controller alerts for stale `submitted`, stale `failed`, and
  near-expiry leases.

## Metrics

When `executor.metrics_addr` is set (e.g. `:9092`), `reconcile-loop` exposes
Prometheus metrics on `/metrics`:

- `livepeer_pool_payout_executor_transaction_submitted_total{outcome}` —
  counter of dispatch attempts. `outcome` is one of `succeeded`, `failed`,
  `skipped`, `dry_run`, `backoff_skipped`.
- `livepeer_pool_payout_executor_transaction_confirmed_total{outcome}` —
  counter of confirmation results. Same outcome vocabulary; also includes
  `pending` for confirmations still waiting for sufficient confirmations.
- `livepeer_pool_payout_executor_reconcile_iteration_total{outcome}` —
  counter of reconcile-loop iterations (`success` or `error`).
- `livepeer_pool_payout_executor_reconcile_iteration_duration_seconds` —
  histogram of wall-clock time per `reconcileOnce`.

If `executor.metrics_addr` is empty, the listener is not started.

## Mapping controller alerts to executor recovery steps

`pool-controller` emits payout alerts via `GET /admin/v1/payout-alerts`.
Each alert maps to a concrete executor command:

| Controller alert kind | Symptom | Recovery step |
|---|---|---|
| `submitted_stale` | Intent in `submitted` past expected confirmation window | `confirm-submitted` (or wait for the next reconcile-loop iteration). If still pending after several iterations, inspect the chain for the `tx_hash`. |
| `failed_stale` | Intent in `failed` past `requeue_cooldown_seconds` | If `auto_requeue_failed: true`, the loop will pick it up. Otherwise inspect `failure_reason` and run `requeue-failed` or `requeue-alerted-failed`. |
| `lease_expiring_soon` | Lease about to expire mid-batch | Loop renews automatically. If you see this persist, check executor wallet liveness and RPC latency. |
| `retry_limit_reached` | Intent failed `max_retries` times | Manual triage: read `failure_reason`, decide between operator-side requeue (after fixing root cause) and `mark-failed-fatal` to remove it from the auto-loop. |

Common root causes by failure_reason substring (best inspected with
`list-alerts` + `list-intents --status failed`):

- `nonce too low` / `nonce too high` — wallet contention; ensure only one
  executor instance is running, then `requeue-failed`
- `insufficient funds` — fund the payout wallet, then `requeue-failed`
- `replacement transaction underpriced` — wait for the original to confirm
  or invalidate; do not double-submit
- `keystore: decrypt` — bad password file path; fix config, restart

## Detecting executor failure pressure

| Symptom on dashboard | Likely cause | First step |
|---|---|---|
| `reconcile_iteration_total{outcome="error"}` rising | RPC, controller, or keystore unreachable | Inspect stderr / journal logs for the underlying error |
| `transaction_submitted_total{outcome="failed"}` rising without `succeeded` | Wallet- or chain-side issue (insufficient funds, gas spike, nonce reuse) | See alert table above |
| `transaction_confirmed_total{outcome="pending"}` sustained | Confirmation threshold high relative to chain tail latency; or stuck submitted tx | Reduce `confirmation_blocks` if appropriate; otherwise inspect chain |
| `reconcile_iteration_duration_seconds` p99 rising | RPC slow or controller slow | Watch RPC + controller latency before scaling executor |

## Recovery notes

- Persist `executor.state_path` if you want reconcile-loop retry/backoff state
  and run history across restarts.
- Owned valid leases are resumed on restart before new exported work is
  claimed.
- Fatal preflight failures release leases immediately back to
  `pool-controller`.

## Backup scope

Back up:

- the executor BoltDB file if used
- the keystore file

Do not back up the keystore password in the same place as the keystore.
