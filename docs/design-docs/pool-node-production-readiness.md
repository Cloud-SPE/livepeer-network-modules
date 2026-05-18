# Pool Node Production Readiness

This checklist is the operator-facing production gate for plan 0029. It is
cross-cutting: it binds `pool-controller`, `pool-reconciler`,
`pool-payout-executor`, `capability-broker`, `payment-daemon`, and
`protocol-daemon`.

## Current implementation status

Implemented and validated:

- `pool-controller` persists member config, receipts, round closes, payout
  intents, retry history, lease state, and operator summaries.
- `pool-reconciler` closes rounds durably from `protocol-daemon` round timing,
  `payment-daemon` confirmed revenue, and `pool-controller` final work
  receipts.
- `pool-payout-executor` signs native-`ETH` payouts on Arbitrum from a
  keystore-backed hot wallet and writes `submitted` / `paid` / `failed` state
  back to `pool-controller`.
- A real Arbitrum dust payout was executed successfully:
  - amount: `1000000000000 wei` (`0.000001 ETH`)
  - destination: `0x0AfC5F4500Ce63aA5f029a78C3633AFe0B77af99`
  - tx hash:
    `0x1aaaf3d32b58e5862621960070eb2523ff67c1fac4425c7c3852c19673de149b`

That means the remaining work is mostly operations, policy hardening, and
longer-running validation rather than missing component plumbing.

Recovery/runbook status:

- control-plane, broker-apply, and publish recovery playbooks now live in
  `docs/design-docs/pool-orchestrator-production-rollout.md`
- payout/reconciler recovery playbooks now also live there for:
  - stalled round close
  - stale submitted payouts
  - failed payout accumulation
  - stuck or near-expiry leases

## Release checklist

### 1. Topology and persistence

- Decide whether v1 runs single-instance for:
  - `pool-controller`
  - `pool-reconciler`
  - `pool-payout-executor`
- Persist these state paths on durable volumes:
  - `pool-controller --data-dir`
  - `pool-reconciler reconcile.state_path`
  - `pool-payout-executor executor.state_path`
- Define backup and restore procedures for all three BoltDB stores.
- Document restart order and restart behavior.
- Validate the component compose files and the combined scenario:
  - `pool-controller/compose/docker-compose.yml`
  - `pool-reconciler/compose/docker-compose.yml`
  - `pool-payout-executor/compose/docker-compose.yml`
  - `infra/scenarios/pool-node/docker-compose.yml`
  - `infra/scenarios/pool-orchestrator/docker-compose.yml`

### 2. Secrets and wallet operations

- Store the executor keystore outside git and outside image layers.
- Deliver the keystore password through the deployment secret system, not a
  checked-in file.
- Define hot-wallet funding policy:
  - target balance floor
  - refill owner
  - refill process
- Decide whether gas is pure operator overhead or needs external accounting
  visibility.
- Verify `pool-controller` admin bearer auth is enabled in production.

### 3. Retry and failure policy

- Confirm whether unattended auto-requeue stays enabled or disabled in the
  production executor config.
- Confirm the v1 retry policy:
  - `max_retries`
  - `requeue_cooldown_seconds`
  - which failure classes are transient
- Decide whether heuristic failure classification is acceptable, or whether
  payout failure classes must become explicit structured values.

### 4. Monitoring and alerting

- Collect logs from:
  - `pool-controller`
  - `pool-reconciler`
  - `pool-payout-executor`
- Alert on:
  - stale `submitted` intents
  - long-lived `failed` intents
  - leases near expiry
  - retry-limit alerts
  - low payout-wallet balance
  - missed round-close progression
- Scrape and retain the component metrics surfaces where available.

### 5. Privacy and product policy

- Decide whether `GET /public/v1/member-payouts` remains public in production.
- If not, define member auth or signed-access policy before rollout.
- Decide whether any members require manual payout holds or exclusions.

### 6. Runtime validation

- Run at least one staging payout round with:
  - real `pool-reconciler`
  - real `pool-controller`
  - real `pool-payout-executor`
  - real Arbitrum RPC
  - funded but low-balance hot wallet
- Run multiple-round soak validation:
  - repeated round-close submissions
  - repeated payout cycles
  - restart recovery during `leased`
  - restart recovery during `submitted`
- Validate operator intervention flows:
  - `list-alerts`
  - `requeue-failed`
  - `requeue-alerted-failed`
  - lease expiry / release / renewal

## Recommended production defaults

- Start with one instance each of:
  - `pool-controller`
  - `pool-reconciler`
  - `pool-payout-executor`
- Keep executor batching conservative:
  - `batch_size: 1` for initial rollout
  - increase only after repeated clean rounds
- Keep auto-requeue conservative:
  - `auto_requeue_failed: false` initially, or
  - `max_retries: 3`
  - `requeue_cooldown_seconds: 3600`
- Use a dedicated Arbitrum payout wallet, not the Pool cold identity.

## Exit criteria for “production ready”

This Pool node stack can be called production ready when:

- the three Pool components run from durable persisted state,
- secrets and keystore handling are documented and deployed safely,
- alerting exists for payout and round-close failure modes,
- at least one real staging payout has succeeded,
- repeated multi-round soak runs are clean,
- retry/privacy/wallet policies are explicit rather than implicit.
