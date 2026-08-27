# pool-controller

`pool-controller` is the Pool-side control-plane component. It holds the pool's
policy, decides what runs where, and owns the money. It is deliberately *not*
in the request data path: if it is down, the broker keeps serving from what it
last accepted.

The current design is the connected-worker reset of
`docs/exec-plans/active/0040-pool-template-connected-worker-reset.md`, extended
by `docs/exec-plans/active/0043-connected-runners-and-offer-manifest.md` and
`docs/exec-plans/active/0044-zero-touch-pool-onboarding.md`. The narrative
version, with the flows drawn out, is
[`docs/design-docs/pool-overlay-flows.md`](../docs/design-docs/pool-overlay-flows.md).

## The model in five sentences

A member signs in with an ETH wallet, enrols a host, and runs one command; the
bundle contains only `pool-member-agent`. The operator's single policy gesture
is **which workload templates are enabled at what price** — templates are YAML
files in the repo-root `templates/` catalog, and per-pool state is nothing but
`{enabled, price, extra}` overrides. An enabled, priced template is *derived*
into an offer and pushed to every broker in the fleet; nothing stores an offer.
Placement is policy over declared facts — a GPU's class, the templates it
satisfies, the member's opt-outs — so no operator picks what runs on a card,
and every decision carries a reason code. The agent then pulls its own desired
state and reconciles the host, the trust ladder advances placements on its own,
and the operator is left with the exceptions: lifting a suspension, overriding
a duplicate GPU claim, banning a member, approving payouts until those
graduate.

> **The shipped catalog cannot start a runner yet.** None of the five templates
> in `templates/` carries a `runner_compose` block — the v1 images and model ids
> are still open (`lnm-v12`) — so the compose service rendered for them has no
> `image`. Everything up to and including the placement decision works; a pool
> that wants a runner to actually start must add `runner_compose.image` to a
> template of its own.

## What it owns

Persisted control-plane state in BoltDB:

- wallet-authenticated pool members, and the sign-in nonces
- host enrollments and enrollment credentials
- GPU hardware inventory
- per-pool template overrides (`{enabled, price, extra}`) and member
  per-template opt-outs
- template assignments — one template placed on one GPU — and their ladder
  state
- certification runs
- accepted work receipts, round receipts, settlement windows, payout batches,
  and payout intents
- backend-selection state and the audit log

Read at boot, not stored: the workload catalog (`template_catalog_dir`) and
`payout-policy.json`. Both are policy, and the way a pool records a decision is
a reviewed diff, not a live edit to a database.

The supported production path is:

- bootstrap-only controller config
- persisted control-plane state in BoltDB
- the offer set and attach credentials pushed to each broker over its admin
  API — nothing is rendered to a broker config file

## The legacy member model is gone

Not merely unsupported: plan 0044 §5 phase A deleted `JoinRequest`,
`MemberBackend`, the old `Assignment`, admission review, backend verification,
auto-approval, auto-drain, `offerservice`, the nested
`members[].backends[].offerings[]` config and its compatibility loader, and
every route and console page that drove them. The pool never dials a
member-supplied endpoint, so there is nothing to verify before admission and
nothing for an operator to approve. Do not add anything back to it.

Operator runbook:
- [`RUNBOOK.md`](./RUNBOOK.md)

## Templates, placement, and desired state

`internal/templates` loads `*.yaml` from `template_catalog_dir` at boot. A
missing directory is fine — an accounting-only controller has no catalog and
must still boot. A *malformed* template is a hard error, because a silently
skipped one leaves a member running nothing with no explanation.

`internal/placement` turns declared facts into placements. A GPU's driver
marketing string normalises to a pool class (`rtx-4090`, `a100`, …; laptop and
Max-Q parts get no class rather than the wrong one). Among templates whose
`requirements` the GPU satisfies and the member has not opted out of, the
highest `priority` takes the primary slot; another stacks alongside only where
it names this class in `stacking.secondary_on` and the class's stance allows a
rider. Members opt **out**, never in — opting in would be a member choosing
what the pool sells.

`GET /admin/v1/placement-plan` shows what that policy would do, with reason
codes on every GPU including the ones that got nothing;
`POST /admin/v1/placement-plan/apply` commits it. A template that should stop
running is drained, not deleted.

`internal/desiredstate` renders each placement into a compose fragment with the
GPU pinned **by UUID** — not `gpus: all`, or two workloads on a two-card host
would both claim both devices — plus the models to fetch and the capability and
identity the runner must declare at attach. The agent fetches that document
(ETag), writes one `runners.compose.yaml`, and reports back. Withdrawal is
sequenced by the agent: `draining` goes into the attach document *before* the
container stops, so the broker stops dispatching while the runner can still
serve (`runner-attach` §7.1).

## The trust ladder

`internal/ladder` runs on a timer (default 60s, `ladder.evaluation_interval_ms`)
and moves placements between trust states:

```
certified ─▶ probationary ─(closed settlement round ∧ ≥N jobs)─▶ active
active ─(score below floor)─▶ throttled ─(recovers)─▶ active
any ─(K consecutive failures)─▶ recertify
any ─(serious failure)─▶ suspended ─(operator lifts)─▶ probationary
```

Promotion needs **both** halves. A job count alone can be run up in minutes by
a host about to fail; a closed round alone proves only that time passed.
Together they are evidence the pool actually billed for.

Every transition records `{state, reason_code, evidence, at}` from a closed set
of reason codes, so an operator reading the audit log and a member reading
their own status page see the same sentence. Weights and caps reach the broker
through the selection snapshot it already polls.

Configuration lives under `ladder:` — `probation_share_ppm`,
`probation_max_in_flight`, `probation_min_jobs`, `exploration_ppm`,
`score_floor`, `recertify_after_failures`, `active_share_cap_ppm`. A zero field
means "not configured" and takes the 0040 §8.3 default; it does not mean zero.

## Listeners

Separated by address, not by an auth check — an operator console reachable from
the same address members use is one misconfigured proxy away from being
reachable *by* them.

- `listen.paid` — operator console, `/admin/*`, and the read-only
  `/public/v1/*` shop window.
- `listen.member` — the member portal and `/member/v1/*`, and nothing else.
  The admin mux is never mounted on it.
- `listen.metrics` — Prometheus, default `:9090`.

Leaving `listen.member` empty keeps both surfaces on the paid listener. That is
the single-address deployment and stays supported.

## Configuration blocks

| Block | What it decides |
|---|---|
| `identity` | The orch address and label this pool speaks as. |
| `template_catalog_dir` | Where the workload catalog is read from at boot. Empty is valid: an accounting-only controller has no catalog. |
| `placement.max_templates_per_class` | How many templates a GPU class runs at once. Omit for the built-in stances. |
| `ladder` | The trust policy (see above). Omit for the 0040 §8.3 defaults. |
| `payouts` | `policy_path`, `pause_path`, `auto_close_windows`, `scale_tolerance`. All empty means approval stays entirely human, which is where a pool starts. |
| `listen` | `paid`, `member`, `metrics`, `worker_quic`. |
| `admin_auth` | `bearer_token_ref` in production; literal `bearer_token` is for local testing. |
| `scoring` | Cooldown, EMA, latency target, warm-up, summary list limits. |
| `bootstrap` | The broker fleet to push to, and the public URLs written into member bundles. |

`examples/pool-controller-config.example.yaml` documents `identity`,
`template_catalog_dir`, `placement`, `admin_auth`, `listen`, `scoring` and
`bootstrap`. It does not yet show `listen.member`, `ladder:` or `payouts:`;
read `internal/config/config.go` for those until it does.

Current scoring boundary:

- repeated `backend_failure` outcomes already use a persisted 5-minute rolling
  window to open Pool cooldown
- `real_success_score` now recomputes from a persisted 5-minute rolling window
  of `success` vs `backend_failure` outcomes, then blends with longer-lived EMA
  memory
- `real_latency_score` now recomputes from a persisted 5-minute rolling window
  of latency observations using a normalized p95-derived signal, then blends
  with longer-lived EMA memory
- EMA memory now uses a 24-hour half-life and drifts toward neutral `0.5`
  between observations
- recovered or newly seeded backends re-enter with warm-up-capped weight
- warm-up now auto-graduates after enough recent routed samples
- manual warm-up overrides are now distinct from automatic warm-up recovery and
  can be cleared without losing automatic state
- the `scoring` config block now lets operators tune cooldown, EMA, latency
  target, warm-up, and summary list limits without code changes
- `scoring.recent_window_stale_after_ms` now controls when older real-traffic
  windows become "stale sample window" routing reasons, and that threshold is
  exported to brokers via the selection snapshot so broker fallback reasoning
  stays aligned

Scoring feeds the ladder rather than replacing it. The scorer answers "how is
this runner doing right now"; the ladder answers "what share of the pool's
traffic and money has it earned", and it is the ladder's answer the broker
routes on.

This component is not in the request data path. If `pool-controller` is down,
the broker keeps serving what it last accepted.

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

Current admin endpoints (paid/admin listener):

Health and state

- `GET /healthz`
- `GET /readyz`
- `GET /metrics` on the metrics listener
- `GET /admin/v1/state`
- `GET /admin/v1/snapshots`
- `POST /admin/v1/reload`
- `GET /admin/v1/audit-events`

Console pages

- `GET /admin`, `GET /admin/pool`, `GET /admin/offers`, `GET /admin/audit`
- `GET /admin/login`, `POST /admin/login`, `POST /admin/logout`
- `GET /admin/assets/`

Catalog and offers

- `GET /admin/v1/template-catalog` — the loaded template files
- `PUT /admin/v1/template-overrides/{id}` — enable / price / extra
- `DELETE /admin/v1/template-overrides/{id}`
- `GET /admin/v1/offers` — what the enabled templates derive into
- `GET /admin/v1/offerings`

Members, hosts, placement, ladder

- `GET /admin/v1/pool-members`
- `PATCH /admin/v1/pool-members/{address}`
- `GET /admin/v1/host-enrollments`
- `POST /admin/v1/host-enrollments/{id}/revoke`
- `GET /admin/v1/hardware-units`
- `GET /admin/v1/placement-plan` — what placement policy would do, with reasons
- `POST /admin/v1/placement-plan/apply`
- `GET /admin/v1/template-assignments`
- `POST /admin/v1/template-assignments` — direct placement, for the cases
  policy cannot reach
- `POST /admin/v1/template-assignments/{id}/certification/start`
- `GET /admin/v1/certification-runs`
- `POST /admin/v1/certification-runs/{id}/complete`
- `POST /admin/v1/ladder/run` — run a ladder pass now instead of on the timer
- `GET /admin/v1/exceptions` — the operator queue: suspensions, duplicate GPUs

Scoring

- `GET /admin/v1/scoring-settings`
- `GET /admin/v1/backend-selection-snapshot`
- `GET /admin/v1/backend-selection-summary`
- `POST /admin/v1/backend-outcomes`
- `POST /admin/v1/backend-overrides/quarantine` · `/clear-quarantine`
- `POST /admin/v1/backend-overrides/drain` · `/clear-drain`
- `POST /admin/v1/backend-overrides/warmup` · `/clear-warmup`
- `POST /admin/v1/backend-overrides/max-share-cap` · `/clear-max-share-cap`

Receipts, settlement, payouts

- `GET`/`POST /admin/v1/work-receipts`
- `GET`/`POST /admin/v1/round-receipts`
- `POST /admin/v1/round-close`
- `GET /admin/v1/settlement-windows`
- `POST /admin/v1/settlement-windows/close`
- `GET /admin/v1/payout-batches`
- `POST /admin/v1/payout-batches/{id}/approve`
- `POST /admin/v1/payout-batches/{id}/policy-review` — what
  `payout-policy.json` says about this batch
- `GET /admin/v1/payout-policy`
- `GET /admin/v1/payout-intents`
- `POST /admin/v1/payout-intents/derive` · `/export` · `/claim` · `/renew` ·
  `/release` · `/requeue` · `/status`
- `GET /admin/v1/member-payouts`
- `GET /admin/v1/payout-rounds`
- `GET /admin/v1/payout-alerts`

Current member endpoints (member listener, or the paid listener when
`listen.member` is unset):

- `POST /member/v1/auth/nonce`
- `POST /member/v1/auth/verify`
- `POST /member/v1/enrollments`
- `GET /member/v1/enrollments/{id}/bundle`
- `POST /member/v1/enrollments/{id}/hardware`
- `GET /member/v1/enrollments/{id}/desired-state` — what this host should run
  (ETag; the revision doubles as the tag, so a quiet host mostly gets 304)
- `POST /member/v1/enrollments/{id}/status` — the agent's report of what it
  achieved
- `GET /member/v1/enrollments/{id}/status` — hosts, GPUs, placements, and the
  ladder state **with its reason code and evidence**
- `GET /member/v1/enrollments/{id}/earnings`
- `GET`/`POST /member/v1/enrollments/{id}/opt-outs`,
  `DELETE /member/v1/enrollments/{id}/opt-outs/{optOutID}`
- `POST /member/v1/enrollments/{id}/rotate` — new enrollment credential; the
  agent calls this itself, well inside the token's lifetime
- `POST /member/v1/enrollments/{id}/retire` — placements drain first

All of them are enrollment-token authenticated and scoped to that enrollment.
Privacy is the rule that shapes the contract: a member sees their own amounts,
never a share of a pool total. A share plus a public total is another member's
income by subtraction.

There is no `/member/v1/join-requests` surface any more; wallet sign-in plus a
host enrolment is the whole member-facing path.

The member portal's HTML, CSS and JS are in `internal/ui/web/`, but the routes
that serve those pages and their assets have not landed yet (`lnm-6at.12`).
The API above is complete.

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

Synthetic probes are gone. The controller used to dial member backends with
its own probe traffic to decide whether they were healthy; it cannot do that
any more, and should not want to. Members are outbound-only, so there is no
address to dial, and the question a probe answered is now answered better by
two things that already exist: **certification**, which the broker runs over
the runner's own attach connection using the steps the template declares, and
the **ladder**, which judges a placement on real billed work rather than on
synthetic traffic nobody paid for. `synthetic_probes` config, the
`/admin/v1/synthetic-probes/run` route, and the per-family probe recipes were
all deleted with that path.

Offer protocol vocabulary:

- Offers declare `protocol` (a `<name>/v<major>` tag such as `paid-job/v1`),
  which replaced the removed v0 `interaction_mode` field.
- Paid-job offers may declare `job.transports` (any of `unary`, `stream`,
  `multipart`). An offer that declares none is rendered as
  `job: {transports: [unary]}` -- the narrowest safe assumption for the
  request/response workloads pool members serve. Since plan 0043 the
  transports an offer advertises come from the runner that freezes it,
  so this default only affects admission-time validation.
- `paid-session/*` offers are rejected at admission: the pool member contract carries no runtime descriptor schema or
  runner create/status/terminate paths, and pool-controller configures neither
  `external_base_url` nor `session_store` -- the broker requires all of them
  before it will load a session capability.

Current Pool media limitation:

- HTTP request/response, HTTP streaming, and HTTP multipart templates can run
  through connected workers.
- WebSocket, RTMP, and remote-runner drivers remain broker-supported, but Pool
  acceptance tests should only exercise them once a template family requires
  them.
- WebRTC media-plane workloads need a separate ICE/TURN design before Pool
  worker support; UDP/SRTP is not solved by the connected-worker byte-stream
  tunnel.

Broker push contract (plan 0043, extended by 0044):

- `pool-controller` pushes what it owns to **every broker in the fleet**
  (`bootstrap.brokers`, or the single-broker `bootstrap.broker_admin_url` a
  dev deployment can keep using) over the admin API:
  `PUT /admin/v1/offers` (the offer set) and `PUT /admin/v1/credentials`
  (the credentials that may attach, as hashes only). Both are full
  replacements and idempotent; a credential that disappears from a push
  is a revoke, which closes that host's connections.
- The offer set is **derived, not stored**: it is exactly the enabled,
  priced templates from the catalog. Enabling a template or changing its price
  override is therefore the whole of the operator's gesture — there is no
  separate offer record to keep in step with it.
- Offers are pushed before credentials, so a host whose credential was
  just accepted attaches into a broker that already knows what it might
  serve.
- There is no rendered broker config file, no staging command, and no
  reload: runners tell the broker what they are, and the broker freezes
  those facts into the offer. `brokerrender`, `runtimeservice`, the
  `/admin/v1/broker-runtime/*` routes and `bootstrap.broker_apply_command`
  are deleted.
- The recorded runtime revision carries `push_error` when the broker
  refused (naming the offer and field), and `changed_offers` /
  `revoked_hosts` when it accepted. A failed push leaves the broker
  serving what it last accepted, so paid traffic and the signed manifest
  are unaffected.
- Runner state — who is attached, what they declared, how they certified
  — is read back from the broker (`GET /admin/v1/runners`,
  `/admin/v1/certification`) and surfaced on the coordinator's console.

Current public endpoints:

- `GET /public/v1/summary`
- `GET /public/v1/rounds`
- `GET /public/v1/offerings`
- `GET /public/v1/member-payouts?member_eth_address=0x...`

These are registered on the admin/paid listener, not the member one. The member
listener deliberately serves no cross-member figure — a member reads their own
earnings authenticated, through `GET /member/v1/enrollments/{id}/earnings`.

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
- `livepeer_pool_work_receipt_status_total{status}`
- `livepeer_pool_round_receipt_total`
- `livepeer_pool_payout_intent_status_total{status}`
- `livepeer_pool_receipt_write_total{kind,status}`
- `livepeer_pool_payout_intent_action_total{action,status}`
- `livepeer_pool_payout_intent_failed_age_seconds_max`
- `livepeer_pool_payout_intent_retry_count_max`
- `livepeer_pool_payout_intent_with_retries_total`

The `livepeer_pool_synthetic_probe_*` families are gone with the probes
themselves.

## Operator-workflow policy automation

There is no `policy:` config block and no policy worker. The opt-in rules that
lived here — `auto_approve_join_requests`, `auto_drain_backends`,
`backend_failure_rate_threshold`, `backend_min_samples`,
`evaluation_interval_ms` — were deleted with the legacy member model, because
the gestures they automated no longer exist: there is no join request to
approve, and no `MemberBackend` to drain.

The equivalents in the new model are:

- **Instead of auto-approving members:** the operator sets policy once, by
  enabling templates and pricing them. An enabled, priced template becomes an
  `offers[]` entry pushed to the broker, and GPUs are matched to it
  deterministically (plan 0044 §3.3).
- **Instead of auto-draining backends:** a poor-scoring *template assignment*
  on a GPU is throttled, forced to recertify, or suspended by the automatic
  ladder in plan 0044 §3.5, with a reason code and evidence on every
  transition. Only lifting a suspension is an operator gesture.

Both have landed. `internal/placement` decides placements from declared facts,
and `internal/ladder` advances them on a timer — the automation now lives in
named policy engines rather than in a `policy:` block of loose thresholds,
which is the point: each decision is reproducible from its inputs and carries
the reason code that justified it.

What is *not* automated, deliberately:

- **Applying a placement plan.** `POST /admin/v1/placement-plan/apply` is a
  call, not a loop. Placement is deterministic, so the plan is worth reading
  before it is committed — and the endpoint that shows it
  (`GET /admin/v1/placement-plan`) exists for exactly that.
- **Approving a payout batch.** Human by default. `payout-policy.json` can
  take it over within bounds, and the graduation plan for widening those
  bounds is in [`RUNBOOK.md`](./RUNBOOK.md).
- **Closing a settlement window.** `internal/service/settlement/autoclose.go`
  implements the hold-on-anomaly decision and `payouts.auto_close_windows` /
  `payouts.scale_tolerance` exist in config, but **nothing calls them yet**:
  closing a window is still `POST /admin/v1/settlement-windows/close`.

## Commands

Normal production operations should use:

- `serve`
- admin/member APIs

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
