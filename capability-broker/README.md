# capability-broker

The Go reference implementation of the workload-agnostic capability broker
defined in [`../livepeer-network-protocol/`](../livepeer-network-protocol/).

> **For agents:** start at [`AGENTS.md`](./AGENTS.md).

## What it is

One process per orch host. Reads a single declarative `host-config.yaml`,
exposes:

- `POST /v1/job` — paid one-shot exchanges (`paid-job/v1`; unary, stream, multipart).
- `POST /v1/session` + `/v1/session/{id}[/topup|/end|/events]` — paid durable sessions (`paid-session/v1`).
- `POST /v1/payment/ticket-params` — unpaid quote-free ticket-params proxy for sender-mode payment daemons.
- `GET /registry/offerings` — capability inventory for orch-coordinator scrape.
- `GET /registry/health` — live capability availability for gateway resolvers.
- `GET /healthz` — process health.
- `GET /admin/v1/runtime` — private runtime status, including loaded revision.
- `POST /admin/v1/runtime/reload` — private runtime reload endpoint.
- `GET /internal/v1/worker/session` — connected-worker WebSocket attach.

Plus `GET /metrics` on a **separate** metrics listener (`--metrics`,
default `:9090`) — deliberately not mounted on the paid listener, so
scrapes never traverse the payment middleware chain.

Dispatches inbound requests to backends declared in `host-config.yaml`.
Validates payment via a co-located `payment-daemon` (over unix socket; a
stub client is available for dev via `payment_daemon.mock`).

Work units come from a different place per protocol, and the config grammar
enforces the split:

- `paid-job/v1` meters the exchange the broker forwarded, so
  `work_unit.extractor` is **required**.
- `paid-session/v1` meters runner-reported cumulative claims, so
  `work_unit.extractor` is **rejected** — there is no exchange for an
  extractor to run on.

Declaring any `paid-session/v1` capability also makes `session_store`
(durable bbolt path + sealing key) and `external_base_url` required; see
[`docs/operator-runbook.md`](./docs/operator-runbook.md) §2.

Repeated `capabilities[]` entries with the same `(id, offering_id)` are
treated as one published offering with multiple runtime backend candidates.
`/registry/offerings` dedupes the published tuple; request dispatch selects a
currently-eligible backend at runtime.

### `offers[]` — the plan-0043 grammar

`offers[]` is the operator-only grammar that replaces `capabilities[]`
(plan 0043 §3.1). An offer carries what is sold and nothing about the
runner: `offering_id`, `capability`, `protocol`, a `match` selector over
attached runners' declared identity, `price`, `capacity`, `extra`,
`extra_from_runner` (which runner `x-*` keys are promoted), an optional
`session_policy` (the operator-owned paid-session axes), and
`certification[]` (the steps every matched runner must pass —
[`certification-steps.md`](../livepeer-network-protocol/protocols/certification-steps.md)).
Runner facts — transports, work unit, extractor, paths, readiness, model
identity — arrive in the runner's attach document
([`runner-attach.md`](../livepeer-network-protocol/protocols/runner-attach.md))
and are frozen into the offer by the first certified runner.

`offers_source: file` (default) means this file owns them;
`offers_source: admin` means a pool-controller pushes them over
`PUT /admin/v1/offers` ([`broker-admin.md`](../livepeer-network-protocol/protocols/broker-admin.md) §4.2)
and requires `admin_auth`. See
[`examples/host-config.offers.example.yaml`](./examples/host-config.offers.example.yaml).

**Status:** the grammar is validated at load and coexists with
`capabilities[]` (an offering lives in one or the other). Attach, match,
freeze, and dispatch over attached runners land with plan 0043 items 6–8;
until then an offer is configuration only, and `capabilities[]` still
drives the paid path. `capabilities[]` is deleted when item 8 ships.

When `receipt_sink.url` is configured, the broker also emits best-effort Pool
work receipts to `pool-controller`:

- `stub` after backend selection
- `final` after post-request unit reconciliation

Receipt-sink failures are logged but do not fail paid requests.
If `pool-controller` enables `admin_auth`, configure the matching bearer token
on `receipt_sink.auth`.

Broker runtime admin surface:

- `GET /admin/v1/runtime` and `POST /admin/v1/runtime/reload` are intended for
  private/operator use only.
- They stay disabled unless `admin_auth.method: bearer` is configured in
  `host-config.yaml`.
- The bearer token must come from `admin_auth.secret_ref: env://...`.
- `POST /admin/v1/runtime/reload` validates the reloaded config before swap and
  preserves the previous runtime if reload fails.
- Successful reloads keep matching backend health/probe state when possible and
  resume health probing and periodic metadata refresh against the new runtime
  without requiring a broker restart.
- `GET /admin/v1/runtime` now includes a bounded recent reload history so
  operator tooling can inspect the latest broker-local attempts directly.
- Each reload attempt is stamped with a broker-local `attempt_id`; controller
  apply confirmation now checks both that `loaded_revision` matches the desired
  revision and that the broker reports the same reload attempt it just
  triggered.

When `pool_snapshot.url` is configured, the broker also polls
`pool-controller`'s backend-selection snapshot and exposes per-backend snapshot
freshness / entry state plus offering-level Pool aggregates under
`GET /registry/health`. The current Plan 0030
slice also lets Pool snapshot state affect multi-backend selection:

- `expired`, `bootstrap_pending`, and `fetch_error` snapshots fail closed
  for Pool-managed backends
- missing snapshot entries are ineligible
- `excluded` and `quarantined` entry states are ineligible
- `eligible` / `degraded` entries scale broker-local health weight by the
  snapshot's `effective_selection_score`

For connected Pool workers, `pool-controller` renders backend URLs as
`worker://{template_assignment_id}`. The broker treats those as virtual
backends reached through an outbound worker session instead of dialing a public
member URL. Worker sessions are authenticated with the rendered
`backend.worker_session_credential` and expose assigned local runner services
through either:

- `listen.worker_quic` over QUIC, preferred for multiplexed streaming and large
  multipart bodies; or
- `GET /internal/v1/worker/session` over WebSocket as the egress-friendly
  fallback.

The runtime admin surface also exposes connected-worker operations:

- `GET /admin/v1/worker-sessions`
- `POST /admin/v1/worker-sessions/{backend_id}/kill`

### Credential store (plan 0043)

With `credential_store` configured, runners attach with credentials the
broker minted or a pool-controller synced, instead of a per-backend config
string ([`docs/design-docs/credential-store.md`](./docs/design-docs/credential-store.md);
contract in [`broker-admin.md`](../livepeer-network-protocol/protocols/broker-admin.md) §5):

- `POST /admin/v1/enroll` — mint a credential for one host; the token is in
  this response and nowhere else (`Livepeer-Request-Id` replays it).
- `GET /admin/v1/credentials`, `GET /admin/v1/credentials/{id}` — no secrets.
- `POST /admin/v1/credentials/{id}/rotate` — new token, old valid for `grace_seconds`.
- `POST /admin/v1/credentials/{id}/revoke` — delete the hash and close the host's connections.
- `PUT /admin/v1/credentials` — pool sync of hashes; a dropped entry is a revoke.

The store holds `sha256(token)` only, sealed at rest. Attach auth consults
it first on both the WebSocket and QUIC paths, then the legacy
`worker_session_credential` until `capabilities[]` is deleted.

Per-backend `max_in_flight` is enforced before dispatch. For long-lived
remote-runner sessions, the capacity slot is held until session finalization.
`queue_limit` is rendered for policy visibility and future queueing, but v1
dispatch currently fail-fasts when no eligible capacity is available.

**This binary contains zero capability-specific code.** All workload knowledge
lives in the two protocol engines and the extractor implementations, both
standardized in the spec.

## Status

**Shipped.** Two protocol engines (`paid-job/v1` on `POST /v1/job`,
`paid-session/v1` on `/v1/session/*`), 8 extractors, a durable bbolt state
store backing session authority and job idempotency, and the broker-side
interim-debit ticker are all in. The v0 seven-mode interaction taxonomy —
its drivers, the RTMP/HLS media pipeline, and the WebRTC/session-control
pass-through — was removed in 2026-08. See PLANS.md "Code shipping today"
§`capability-broker/` for the canonical summary.

## Build

Per repo-root core belief #15, every gesture is Docker-first.

```bash
make build               # build tztcloud/livepeer-capability-broker:v2.0.0
make run                 # run with examples/host-config.example.yaml
make help                # show all targets
```

No host Go install required.

## Configuration

A single declarative YAML file: [`examples/host-config.example.yaml`](./examples/host-config.example.yaml).
The example starts with minimal `paid-job/v1` entries for smoke bring-up
and keeps more involved shipped shapes commented out until you wire the
necessary backend infrastructure.

For OpenAI-compatible offerings, use the base capability family in `id`
(`openai:chat-completions`, `openai:embeddings`, etc.) and put model identity
in `extra.openai.model`. Deprecated suffixed forms such as
`openai:chat-completions:<model>` are rejected by config validation.

Current broker validation for `openai:*` offerings requires:

- `extra.openai.model`
- `extra.provider`

Optional stable enrichment fields are:

- `served_model_name`
- `backend_model`
- `features.*` (booleans only)

For `provider: "vllm"` and `provider: "ollama"` on HTTP OpenAI-compatible
backends, the broker probes `GET /v1/models`. When the configured
`extra.openai.model` is present upstream, the broker fills missing
`served_model_name`, `backend_model`, and capability-appropriate
`features.*` fields in `/registry/offerings`. Operator-declared values still
win; discovery fills gaps only.

The same overlay pattern also applies to backend families that expose a
stable metadata or options surface. Historical in-repo examples included
audio, video, and vtuber workload binaries; those component trees have been
removed, but the broker-side discovery pattern remains available for any
backend that still exposes the corresponding contract.

The broker refreshes eligible metadata periodically while running. Per-offering
refresh status and the last discovery result are exposed on
`GET /registry/health` under each capability's `metadata` object for every
family that participates in discovery.

Use `--metadata-refresh-interval=<duration>` to tune that cadence. The default
is `5m`. Set a negative duration to disable periodic refresh after the initial
startup discovery pass.

Current `metadata.last_result` values are family-aware. Examples:

- `model_not_found`
- `models_probe_failed`
- `audio_options_empty`
- `audio_options_probe_failed`
- `video_presets_empty`
- `video_presets_probe_failed`
- `vtuber_options_empty`
- `vtuber_options_probe_failed`

Prometheus also exposes
`livepeer_metadata_refresh_total{family,provider,result}` so discovery
regressions are visible without polling `GET /registry/health`.
It also exposes:

- `livepeer_metadata_refresh_duration_seconds{family,provider,result}`
- `livepeer_metadata_refresh_last_attempt_timestamp_seconds{family,capability,offering,provider}`
- `livepeer_metadata_refresh_last_success_timestamp_seconds{family,capability,offering,provider}`
- `livepeer_metadata_refresh_last_success_age_seconds{family,capability,offering,provider}`
- `livepeer_metadata_refresh_current_result{family,capability,offering,provider,result}`
- `livepeer_metadata_refresh_consecutive_failures{family,capability,offering,provider}`

`GET /registry/health` also exposes metadata-level `consecutive_failures` per
offering so the human-facing status surface and Prometheus stay aligned.
On unhealthy refreshes, `last_success_at` is preserved rather than overwritten,
so the age gauge measures time since the last healthy metadata refresh.
The same health payload now includes metadata-level `last_success_age_seconds`
for operators who are inspecting JSON directly instead of scraping metrics.
When repeated published tuples are configured, the same health payload exposes a
`backends[]` array per published capability so operator tooling can see each
candidate backend's individual status. When `pool_snapshot.url` is configured,
each backend may also include a `pool` object with snapshot freshness
(`fresh`, `stale`, `expired`, `bootstrap_pending`, or `fetch_error`), cached
timestamps, and whether the latest snapshot contained an entry for that
backend+offering tuple. In that mode, `selection_eligible`,
`selection_weight`, and `selection_reason` describe the broker's final
Pool-aware routing decision, not just broker-local probe health. If every
backend for a published tuple is blocked by Pool snapshot state or Pool
exclusion state, the tuple-level `status`/`reason` in `/registry/health` also
drop out of `ready` so downstream resolvers stop routing an unroutable tuple.
When the Pool snapshot includes `routing_reason`, broker health reuses that
controller-owned explanation for eligible/degraded/excluded Pool states; the
broker only invents reasons for snapshot transport failures such as
`pool_snapshot_expired` or `pool_snapshot_fetch_error`. The same `pool` blocks
now also expose the controller-supplied scorer knobs that shape those
decisions, including cooldown duration/trigger, EMA half-life, latency
target, stale-sample-window threshold, window-vs-EMA weights, and warm-up
modifier/exit samples. That makes broker `/registry/health` a read-only
mirror of the live controller routing policy without forcing operators to
cross-check controller admin endpoints. The same Pool blocks also expose the
broker's own snapshot timing policy for that control plane, including
snapshot timeout, poll interval, stale threshold, and expiry threshold, so
one health payload explains both the controller's scoring policy and the
broker's local freshness contract.

The Prometheus surface now mirrors that Pool snapshot state too. In addition
to the existing request and metadata families, broker `/metrics` now includes:

- `livepeer_pool_snapshot_cache_status{status}`
- `livepeer_pool_snapshot_generated_timestamp_seconds`
- `livepeer_pool_snapshot_fetched_timestamp_seconds`
- `livepeer_pool_snapshot_setting_seconds{setting}`
- `livepeer_pool_snapshot_entry_state_total{capability,offering,state}`
- `livepeer_pool_snapshot_routing_reason_total{capability,offering,routing_reason}`
- `livepeer_pool_snapshot_automatic_warmup_total{capability,offering}`
- `livepeer_pool_snapshot_cooldown_total{capability,offering}`
- `livepeer_pool_snapshot_average_recent_window_age_seconds{capability,offering}`
- `livepeer_backend_outcome_emit_total{outcome,result}`
- `livepeer_work_receipt_emit_total{status,result}`
- `livepeer_backend_selection_final_total{capability,offering,backend_id,reason}`
- `livepeer_backend_selection_denied_total{capability,offering,backend_id,reason}`
- `livepeer_backend_selection_exhausted_total{capability,offering,reason}`

When the broker runs in production, mount your real `host-config.yaml` over
`/etc/livepeer/host-config.yaml` (the default `--config` location).

## Layout

See [`docs/design-docs/architecture.md`](./docs/design-docs/architecture.md)
for the full package tree and dispatch flow.

```
capability-broker/
├── cmd/livepeer-capability-broker/main.go
├── internal/
│   ├── config/         # host-config.yaml loader + validator
│   ├── server/         # HTTP server, middleware, job + session routes
│   ├── sessionengine/  # paid-session/v1 authority (leases, descriptors)
│   ├── sessionstore/   # durable bbolt state: sessions + job idempotency
│   ├── extractors/     # work-unit extractor library (paid-job only)
│   ├── backend/        # outbound forwarding to declared backends
│   ├── health/         # per-backend probes
│   ├── selection/      # backend eligibility + weighting
│   ├── workerconn/     # connected-worker QUIC / WebSocket sessions
│   ├── payment/        # payment-daemon client (mock for dev)
│   └── observability/  # metrics, logging, request-id
├── examples/
│   └── host-config.example.yaml
├── docs/
└── Dockerfile / Makefile / go.mod
```

`internal/` packages reflect what's shipped.
