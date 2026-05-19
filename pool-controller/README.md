# pool-controller

`pool-controller` is the Pool-side control-plane component from plan 0029. It
owns the persisted Pool control-plane state for:

- orch-owned offers
- join requests
- approved members and backends
- backend-to-offer assignments
- desired broker runtime

The supported production path is:

- bootstrap-only controller config
- persisted control-plane state in BoltDB
- broker runtime rendered from that persisted state

Legacy nested `members[].backends[].offerings[]` config is no longer part of the
supported controller surface.

Operator runbook:
- [`RUNBOOK.md`](./RUNBOOK.md)

The first implementation slice is intentionally narrow:

- load a Pool operator config,
- validate member/backend/offering records,
- deterministically render broker config for the Pool's broker,
- persist startup/reload snapshots in BoltDB for operator inspection,
- persist backend-selection state records keyed by member/backend/offering,
- expose a read-only admin snapshot scaffold for future Pool scoring work.
- accept conservative backend-outcome ingests that nudge persisted real-success
  and real-latency scores for future Pool scoring work.
- add an opt-in synthetic probe runner scaffold for in-scope OpenAI families,
  with concrete chat/embeddings probes and partial audio-family coverage.

Current scoring boundary:

- repeated `backend_failure` outcomes already use a persisted 5-minute rolling
  window to open Pool cooldown
- synthetic probes already enforce the 3-failure exclusion threshold
- `real_success_score` now recomputes from a persisted 5-minute rolling window
  of `success` vs `backend_failure` outcomes, then blends with longer-lived EMA
  memory
- `real_latency_score` now recomputes from a persisted 5-minute rolling window
  of latency observations using a normalized p95-derived signal, then blends
  with longer-lived EMA memory
- EMA memory now uses a 24-hour half-life and drifts toward neutral `0.5`
  between observations
- recovered or first-probed backends re-enter with warm-up-capped weight
- warm-up now auto-graduates after enough recent routed samples
- manual warm-up overrides are now distinct from automatic warm-up recovery and
  can be cleared without losing automatic state
- the `scoring` config block now lets operators tune cooldown, EMA, latency
  target, warm-up, and summary list limits without code changes
- `scoring.recent_window_stale_after_ms` now controls when older real-traffic
  windows become "stale sample window" routing reasons, and that threshold is
  exported to brokers via the selection snapshot so broker fallback reasoning
  stays aligned

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

Run the controller:

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

`listen.metrics` is now active. `pool-controller` serves Prometheus metrics on
that separate listener, defaulting to `:9090`.

Current admin endpoints:

- `GET /healthz`
- `GET /readyz`
- `GET /metrics` on the metrics listener
- `GET /admin/v1/broker-config`
- `GET /admin/v1/members`
- `GET /admin/v1/offerings`
- `GET /admin/v1/state`
- `GET /admin/v1/scoring-settings`
- `GET /admin/v1/backend-selection-snapshot`
- `GET /admin/v1/backend-selection-summary`
- `GET /admin/v1/snapshots`
- `GET /admin/v1/work-receipts`
- `GET /admin/v1/round-receipts`
- `GET /admin/v1/payout-intents`
- `GET /admin/v1/member-payouts`
- `GET /admin/v1/payout-rounds`
- `GET /admin/v1/payout-alerts`
- `POST /admin/v1/work-receipts`
- `POST /admin/v1/backend-outcomes`
- `POST /admin/v1/synthetic-probes/run`
- `POST /admin/v1/round-receipts`
- `POST /admin/v1/round-close`
- `POST /admin/v1/payout-intents/derive`
- `POST /admin/v1/payout-intents/export`
- `POST /admin/v1/payout-intents/claim`
- `POST /admin/v1/payout-intents/renew`
- `POST /admin/v1/payout-intents/release`
- `POST /admin/v1/payout-intents/requeue`
- `POST /admin/v1/payout-intents/status`
- `POST /admin/v1/backend-overrides/quarantine`
- `POST /admin/v1/backend-overrides/clear-quarantine`
- `POST /admin/v1/backend-overrides/drain`
- `POST /admin/v1/backend-overrides/clear-drain`
- `POST /admin/v1/backend-overrides/warmup`
- `POST /admin/v1/backend-overrides/clear-warmup`
- `POST /admin/v1/backend-overrides/max-share-cap`
- `POST /admin/v1/backend-overrides/clear-max-share-cap`
- `POST /admin/v1/reload`

`GET /admin/v1/backend-selection-summary` rolls the current Pool routing state
into operator-focused aggregates:
- totals by state
- grouped averages by member and offering
- `worst_offerings` ranked by unhealthy state concentration and recent failure pressure
- top degraded backends
- top excluded/quarantined backends
- grouped `top_routing_reasons` and `top_exclusion_reasons`
- recent outcome / recent backend-failure counts
- recent-window start/end timestamps and freshness

`GET /admin/v1/backend-selection-snapshot` also includes a canonical
`routing_reason` per backend+offering. This is the controller-owned
explanation for why a backend is currently eligible, degraded, excluded,
quarantined, or in warm-up.

`GET /admin/v1/scoring-settings` is the authoritative runtime view of the
active scorer knobs after defaults and reload have been applied.

Synthetic probe behavior:

- disabled by default via `synthetic_probes.enabled: false`
- background runs only happen when explicitly enabled in config
- `POST /admin/v1/synthetic-probes/run` can trigger a one-shot run on demand
- current concrete probes:
  - `openai:chat-completions`
  - `openai:embeddings`
  - `video:transcode.abr` via `GET /v1/video/transcode/abr/presets`,
    requiring at least one declared preset
- current concrete audio probes:
  - `openai:audio-transcriptions`
  - `openai:audio-translations`
  - `openai:audio-speech`
- other `openai:audio-*` subtypes now fall back to family recipes based on
  `interaction_mode`:
  - `http-multipart@v0` uses the transcription / translation probe recipe
  - `http-reqresp@v0` uses the speech / TTS probe recipe
- unsupported audio families still return skipped results with
  `audio_probe_not_implemented`

Current Pool limitation:

- `video:live.rtmp` is intentionally rejected in `pool-controller` member
  configs. The current Pool member model is backend-runtime-only, while the
  shipped live RTMP path is broker-local `ffmpeg-subprocess` plus broker
  RTMP/HLS listeners. See
  `docs/exec-plans/active/0032-pool-live-rtmp-contract-decision.md`.

Broker runtime apply contract:

- `POST /admin/v1/broker-runtime/apply` is now the primary operator action for
  runtime convergence.
- `GET /admin/v1/broker-runtime` and `GET /admin/v1/broker-runtime/history`
  are the primary read surfaces for current convergence state and recent apply
  attempts.
- When `bootstrap.broker_apply_command` is configured, `pool-controller`
  writes the desired broker config to a temp file, executes that command, then
  re-renders desired state and refuses to mark success if the desired revision
  drifted during the apply attempt.
- When `bootstrap.broker_admin_url` is configured, `pool-controller` also:
  - `POST`s `/admin/v1/runtime/reload`
  - `GET`s `/admin/v1/runtime`
  - requires the broker-reported `loaded_revision` to match the controller's
    desired revision before apply is considered successful
  - requires the broker-reported `last_reload_attempt_id` to match the reload
    attempt triggered by `pool-controller`
- `capability-broker` now exposes broker-local reload history on
  `GET /admin/v1/runtime`, and `pool-controller` mirrors controller-side apply
  history on `GET /admin/v1/broker-runtime/history`.
- Manual runtime endpoints remain available only as fallback/debug controls:
  - `POST /admin/v1/broker-runtime/mark-started`
  - `POST /admin/v1/broker-runtime/mark-failed`
  - `POST /admin/v1/broker-runtime/mark-applied`
  They are not the normal production operator path when broker admin reload is
  configured.
- The apply command receives:
  - `POOL_CONTROLLER_CONFIG_PATH`
  - `POOL_CONTROLLER_BROKER_CONFIG_PATH`
  - `POOL_CONTROLLER_BROKER_DESIRED_REVISION`
  - `POOL_CONTROLLER_BROKER_CONFIG_SHA256`
- The rendered broker YAML is also provided on stdin.
- `bootstrap.broker_apply_timeout_ms` controls the command timeout and defaults
  to `30000`.
- `bootstrap.broker_admin_timeout_ms` controls the broker admin HTTP timeout
  and defaults to `5000`.

Current public endpoints:

- `GET /public/v1/summary`
- `GET /public/v1/rounds`
- `GET /public/v1/offerings`
- `GET /public/v1/member-payouts?member_eth_address=0x...`

`GET /public/v1/summary` now includes a compact `worst_offerings` list so
non-admin dashboards can surface the most unhealthy offerings without exposing
the full admin backend-selection summary. That compact list now includes both
`top_routing_reasons` and `top_exclusion_reasons` for each surfaced offering.

`GET /admin/v1/backend-selection-summary` now also includes:

- `score_distribution` buckets over `effective_selection_score`
- `traffic_share` views derived from `recent_routable_outcome_count`
- per-group `average_recent_routable_outcome_count`

Current Prometheus metric families include:

- `livepeer_pool_backend_selection_state_total{capability,offering,state}`
- `livepeer_pool_backend_selection_routing_reason_total{capability,offering,routing_reason}`
- `livepeer_pool_backend_selection_exclusion_reason_total{capability,offering,exclusion_reason}`
- `livepeer_pool_backend_selection_automatic_warmup_total{capability,offering}`
- `livepeer_pool_backend_selection_cooldown_total{capability,offering}`
- `livepeer_pool_backend_selection_average_effective_score{capability,offering}`
- `livepeer_pool_backend_selection_average_recent_window_age_seconds{capability,offering}`
- `livepeer_pool_scoring_setting{setting}`
- `livepeer_pool_backend_outcome_ingest_total{capability,offering,outcome}`
- `livepeer_pool_synthetic_probe_runs_total{result}`
- `livepeer_pool_synthetic_probe_run_duration_seconds{result}`
- `livepeer_pool_synthetic_probe_results_total{capability,offering,status,reason}`
- `livepeer_pool_work_receipt_status_total{status}`
- `livepeer_pool_round_receipt_total`
- `livepeer_pool_payout_intent_status_total{status}`
- `livepeer_pool_receipt_write_total{kind,status}`
- `livepeer_pool_payout_intent_action_total{action,status}`

## Operator-workflow policy automation

The policy worker runs alongside the synthetic-probe loop and evaluates
opt-in automation rules on every `policy.evaluation_interval_ms` tick
(default 60s). Both rules are off by default; flipping them on in
`policy:` requires no restart in normal operation — the worker reads
the current snapshot each tick.

```yaml
policy:
  auto_approve_join_requests: true
  auto_drain_backends: true
  backend_failure_rate_threshold: 0.5
  backend_min_samples: 20
  evaluation_interval_ms: 60000
```

- `auto_approve_join_requests` — when on, the worker auto-approves any
  pending `JoinRequest` whose admission-review preview already says
  `Approvable`. The reason recorded on the request and on the audit
  event is `auto-approved by policy`. Audit kind:
  `join_request_auto_approved`.
- `auto_drain_backends` — when on, the worker transitions any backend
  in `BackendStatusActive` whose worst per-offering recent failure rate
  (`recent_backend_failure_count / recent_routable_outcome_count`)
  exceeds `backend_failure_rate_threshold` to `BackendStatusDraining`.
  `backend_min_samples` gates the rule on a minimum number of routable
  outcomes in the window so brand-new or quiet backends are not drained
  on a single bad sample. Audit kind: `backend_auto_drained` with
  `failure_rate` and `failure_rate_threshold` in the details.

Auto-disable (member-level suspend) is **not** automated; the worker
only drains backends. An operator must decide whether to keep a
drained backend out or re-enable it.

## Migration-only compatibility commands

Normal production operations should use:

- `serve`
- admin/member APIs
- `POST /admin/v1/broker-runtime/apply`

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
