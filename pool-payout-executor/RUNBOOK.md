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
