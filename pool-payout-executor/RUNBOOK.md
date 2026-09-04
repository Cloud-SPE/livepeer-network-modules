# pool-payout-executor runbook

## Role

`pool-payout-executor` is the native-`ETH` payout worker. It:

- reads exported or leased payout intents from `pool-controller`
- claims or renews leases
- signs Arbitrum transfers from a dedicated hot wallet
- writes `submitted`, `paid`, or `failed` state back to `pool-controller`

## Required runtime inputs

- executor config YAML
- Arbitrum RPC list: `executor.rpc_urls` in the config, or `CHAIN_RPC_URLS`
  in the environment (comma-separated, primary first), which takes
  precedence. Every call fails over between the entries.
- keystore file
- keystore password file
- durable `executor.state_path` if reconcile-loop persistence is desired
- durable `executor.intent_store_path` (default: `payout-intents.db` beside
  `state_path`). This is the transaction intent store: every broadcast's tx
  hash and nonce, so a restart resumes tracking instead of re-sending. Back
  it up with `state_path`; losing it is recoverable (the next confirm pass
  re-adopts submitted payouts from the controller's records) but costs a
  chain lookup per payout.

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

## Upgrading from an executor that sent transactions itself

Stop the executor, upgrade, start. No drain is required: on the first
confirm pass every `submitted` payout the controller holds is adopted into
the intent store from its `tx_hash` and `external_ref` (`nonce-N`) and
tracked to confirmation; nothing is re-sent. A `submitted` payout with a
`tx_hash` no endpoint knows and no recorded nonce stays `submitted` and is
reported `pending_confirmation` with the reason; handle it through the
controller's `submitted_stale` alert as before. Draining first (let
`submitted` reach `paid`/`failed` before upgrading) remains the conservative
choice and is fine.

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
| `submitted_stale` | Intent in `submitted` past expected confirmation window | `confirm-submitted` (or wait for the next reconcile-loop iteration). The intent processor replaces a stalled transaction at the same nonce with bumped gas after `replace_after_seconds`, up to `max_replacements` times, then reports it `failed` with `tx.replacement_exhausted`; a payout that stays pending beyond that window is one whose transaction no endpoint knows and whose nonce the controller never recorded — inspect the chain for the `tx_hash`. |
| `failed_stale` | Intent in `failed` past `requeue_cooldown_seconds` | If `auto_requeue_failed: true`, the loop will pick it up. Otherwise inspect `failure_reason` and run `requeue-failed` or `requeue-alerted-failed`. |
| `lease_expiring_soon` | Lease about to expire mid-batch | Loop renews automatically. If you see this persist, check executor wallet liveness and RPC latency. |
| `retry_limit_reached` | Intent failed `max_retries` times | Manual triage: read `failure_reason`, decide between operator-side requeue (after fixing root cause) and leaving it in `failed` (or re-recording it with `mark-failed` and a terminal `--reason`) so the auto-requeue policy stops picking it up. |

Common root causes by failure_reason substring (best inspected with
`list-alerts` + `list-intents --status failed`):

- `nonce_past` / `nonce too low` — wallet contention; ensure only one
  executor instance is running against this wallet, then `requeue-failed`
- `insufficient_funds` — fund the payout wallet, then `requeue-failed`
- `tx.reverted` — the transfer reverted on-chain (a contract recipient that
  rejects value, typically); fix the destination before any requeue
- `tx.replacement_exhausted` — the transaction and its gas-bumped
  replacements never mined within `replace_after_seconds × max_replacements`;
  check RPC health and gas settings, then `requeue-failed` (the requeued
  payout is a fresh intent at a fresh nonce; the old attempts cannot land
  once a later nonce from this wallet has mined)
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
