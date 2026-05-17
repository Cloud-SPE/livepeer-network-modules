# pool-controller

`pool-controller` is the Pool-side control-plane component from plan 0029. It
owns Pool member records and generates `capability-broker` `host-config.yaml`
for the Pool's edge broker.

Operator runbook:
- [`RUNBOOK.md`](./RUNBOOK.md)

The first implementation slice is intentionally narrow:

- load a Pool operator config,
- validate member/backend/offering records,
- deterministically render broker config for the Pool's broker,
- persist startup/reload snapshots in BoltDB for operator inspection.

This component is not in the request data path. If `pool-controller` is down,
the broker keeps serving the last generated config already loaded in memory.

## Current commands

```bash
make build
make test
make run
```

Compose entrypoint:

```bash
docker compose -f compose/docker-compose.yml up -d
```

Generate broker config:

```bash
docker run --rm \
  -v "$PWD/examples/pool-controller-config.example.yaml:/etc/livepeer/pool-controller.yaml:ro" \
  tztcloud/livepeer-pool-controller:dev \
  generate-broker-config \
  --config /etc/livepeer/pool-controller.yaml
```

Run the minimal admin server:

```bash
docker run --rm \
  -p 8080:8080 \
  -v "$PWD/examples/pool-controller-config.example.yaml:/etc/livepeer/pool-controller.yaml:ro" \
  tztcloud/livepeer-pool-controller:dev \
  serve \
  --config /etc/livepeer/pool-controller.yaml \
  --data-dir /var/lib/livepeer/pool-controller \
  --listen :8080
```

Set `POOL_CONTROLLER_ADMIN_TOKEN` in the container environment when
`admin_auth.bearer_token_ref` is configured. All `/admin/v1/*` endpoints then
require `Authorization: Bearer <token>`.

For production, prefer `admin_auth.bearer_token_ref`. Literal
`admin_auth.bearer_token` is intended for local testing only.

Current admin endpoints:

- `GET /healthz`
- `GET /readyz`
- `GET /admin/v1/broker-config`
- `GET /admin/v1/members`
- `GET /admin/v1/offerings`
- `GET /admin/v1/state`
- `GET /admin/v1/snapshots`
- `GET /admin/v1/work-receipts`
- `GET /admin/v1/round-receipts`
- `GET /admin/v1/payout-intents`
- `GET /admin/v1/member-payouts`
- `GET /admin/v1/payout-rounds`
- `GET /admin/v1/payout-alerts`
- `POST /admin/v1/work-receipts`
- `POST /admin/v1/round-receipts`
- `POST /admin/v1/round-close`
- `POST /admin/v1/payout-intents/derive`
- `POST /admin/v1/payout-intents/export`
- `POST /admin/v1/payout-intents/claim`
- `POST /admin/v1/payout-intents/renew`
- `POST /admin/v1/payout-intents/release`
- `POST /admin/v1/payout-intents/requeue`
- `POST /admin/v1/payout-intents/status`
- `POST /admin/v1/reload`

Current public endpoints:

- `GET /public/v1/summary`
- `GET /public/v1/rounds`
- `GET /public/v1/offerings`
- `GET /public/v1/member-payouts?member_eth_address=0x...`

Current receipt-write contract:

- work receipts are idempotent upserts by `id`
- supported `status` values are `stub` and `final`
- `status=final` requires `actual_units > 0`
- round receipts are idempotent upserts by `id`
- `POST /admin/v1/round-close` derives a round receipt from included final
  work receipt IDs plus Pool revenue / Pool cut inputs
- `POST /admin/v1/payout-intents/derive` derives deterministic per-member
  payout intents from a persisted round receipt by `round_receipt_id` or
  `round_id`
- payout intents are idempotent upserts by deterministic `payout-<round>-<member>`
  IDs and start in `pending`
- `POST /admin/v1/payout-intents/export` marks matching `pending` intents as
  `exported` and returns either JSON or CSV for operator handoff
- `POST /admin/v1/payout-intents/claim` leases matching `exported` intents to
  one executor for a bounded TTL, returning a `lease_id` that must accompany
  later `submitted` or leased-`failed` updates
- `POST /admin/v1/payout-intents/renew` extends a live lease for the same
  executor and `lease_id`
- `POST /admin/v1/payout-intents/release` abandons a live lease back to
  `exported` so another executor can pick it up immediately; it may release
  either the whole lease or a specific subset of leased intent IDs
- `POST /admin/v1/payout-intents/requeue` moves `failed` intents back to
  `exported` for retry and clears stale `external_ref`, `tx_hash`, and
  `failure_reason` metadata so the executor can safely reclaim them
- payout intents now persist explicit `failed_at` timestamps, so alerting and
  future retry policy do not need to infer failure age from `submitted_at`
- payout intents also persist `retry_count` and `last_requeued_at`, so future
  retry policy can key off controller-owned canonical retry history
- `POST /admin/v1/payout-intents/status` lets operators advance exported
  intents into `submitted`, `paid`, or `failed` with audit timestamps and
  failure reason capture; leased intents must present the matching `lease_id`
- payout status updates may also attach executor metadata such as
  `external_ref` and `tx_hash`, which are preserved in intent records and CSV
  exports
- `GET /admin/v1/member-payouts` aggregates payout-intent totals per member
  across `pending`, `exported`, `leased`, `submitted`, `paid`, and `failed`;
  it now also carries retry churn (`retried_count`, `total_retry_count`,
  `last_requeued_at`)
- `GET /admin/v1/payout-rounds` aggregates payout-intent counts and wei totals
  per round so operators can see which closed rounds are fully paid versus
  still exported, leased, submitted, or failed; it now also carries retry
  churn (`retried_count`, `total_retry_count`, `last_requeued_at`)
- `GET /admin/v1/payout-alerts` derives operator-facing anomalies from current
  persisted payout state, including stale `submitted` intents, long-lived
  `failed` intents, `leased` intents nearing lease expiry, retry-limit
  breaches, and failures that happened soon after a recent requeue
