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

Dispatches inbound requests to attached runners over the connection those
runners opened. Validates payment via a co-located `payment-daemon` (over
unix socket; a stub client is available for dev via `payment_daemon.mock`).

Declaring any `paid-session/v1` offer makes `session_store` (durable bbolt
path + sealing key) and `external_base_url` required; see
[`docs/operator-runbook.md`](./docs/operator-runbook.md) §2.

### `offers[]` — the operator grammar

`offers[]` is the operator's entire config surface (plan 0043 §3.1). An
offer carries what is sold and nothing about the runner: `offering_id`,
`capability`, `protocol`, a `match` selector over attached runners'
declared identity, `price`, `capacity`, `extra`, `extra_from_runner`
(which runner `x-*` keys are promoted), an optional `session_policy` (the
operator-owned paid-session axes), and `certification[]` (the steps every
matched runner must pass —
[`certification-steps.md`](../livepeer-network-protocol/protocols/certification-steps.md)).
`offering_id` is unique across the file: one offer per id, and runners
multiply an offer rather than entries doing it.

Runner facts — transports, descriptor schemas, work unit, extractor,
paths, readiness, model identity — arrive in the runner's attach document
([`runner-attach.md`](../livepeer-network-protocol/protocols/runner-attach.md))
and are frozen into the offer by the first certified runner. That split is
also where the metering rule now lives: a `paid-job/v1` runner declares the
extractor that meters the exchange the broker forwarded, while a
`paid-session/v1` runner declares `metering` instead and is rejected at
attach if it sends an extractor — there is no exchange for one to run on.
A runner declaring an extractor the broker does not implement is likewise
rejected at attach, rather than the broker failing a startup check on
config an operator hand-copied.

`offers_source: file` (default) means this file owns them;
`offers_source: admin` means a pool-controller pushes them over
`PUT /admin/v1/offers` ([`broker-admin.md`](../livepeer-network-protocol/protocols/broker-admin.md) §4.2)
and requires `admin_auth`. See
[`examples/host-config.example.yaml`](./examples/host-config.example.yaml)
for the annotated reference, or
[`examples/host-config.offers.example.yaml`](./examples/host-config.offers.example.yaml)
for the shortest thing that runs.

Set `offers_state_path` so frozen shapes survive a restart: a shape that
vanished would re-freeze from whichever runner certified first, which is a
silent manifest change. `accept-shape`, `confirm-published`, `disable` and
`enable` are served under `/admin/v1/offers`; the frozen tuple is published
on `/registry/offerings` stamped with the protocol module's `spec_version`
and carrying `offers_revision`, and runner churn never changes that payload.

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
- Successful reloads keep attached runners and their certification state
  where the new offer set still matches them, so a config edit does not make
  every host re-attach and re-certify.
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

Pool members never expose a public URL. A member's host attaches outbound and
the broker reaches it back down that same connection, through either:

- `listen.attach_quic` over QUIC, preferred for multiplexed streaming and large
  multipart bodies; or
- `GET /internal/v1/worker/session` over WebSocket as the egress-friendly
  fallback.

The WebSocket path keeps its old spelling because it is in every bundle
the broker has minted and every agent already running; renaming it would
strand them.

Attach is authenticated with a credential from the credential store below.
The per-backend `worker_session_credential` config string died with the
`capabilities[]` grammar that declared it, and the connected-worker tunnel
it reached — `worker://` backends, the `backend_ids` register, and the
`GET`/`POST /admin/v1/worker-sessions*` routes — is deleted. `listen.attach_quic`
was spelled `listen.worker_quic` while that listener carried both; the old
key is still read, with a deprecation warning.

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
it on both the WebSocket and QUIC paths; the only other accepted bearer is
the admin token.

### Runner attach (plan 0043)

A runner attaches by opening the attach tunnel — `GET
/internal/v1/worker/session` (WebSocket) or the `listen.attach_quic`
listener — and sending, first, a `register` frame whose
`body` is an attach document
([`runner-attach.md`](../livepeer-network-protocol/protocols/runner-attach.md)).
The broker answers with a `register_result` frame: the document is
accepted or rejected as a whole (unknown non-`x-` field, bad
`contract_version` major, credential rejected, duplicate GPU or
capability), and each capability entry is accepted or rejected on its own
(unknown extractor, unknown readiness probe, missing `schema_versions`,
unmet `requirements`, …) with `declared` and `expected` both named. A
re-sent document replaces the previous one on that connection; the host
is gone on disconnect (kept 24 h as `disconnected` for the console).

- `GET /admin/v1/runners` (`?state=`, `?host_id=`, `?capability_id=`,
  `?include=paths`), `GET /admin/v1/runners/{host_id}`,
  `POST /admin/v1/runners/{host_id}/disconnect`.
- `GET /admin/v1/certification` (`?host_id=`, `?offering_id=`, `?state=`,
  `?latest=true`), `GET /admin/v1/certification/{host}/{offering}`,
  `POST /admin/v1/certification/{host}/{offering}/run`.
  `certification_fixtures_dir` resolves `fixture: {ref}` files for
  multipart steps (point it at
  `livepeer-network-protocol/extractors/fixtures` in the image).

Attached runners are matched to `offers[]`, certified by the step engine
(`certification[]` on the offer, executed over the attach connection:
`readiness` uses the runner's own declared probe, `request` drives a
real exchange with `{{identity.*}}`/`{{offer.*}}` substitution and
JSONPath asserts — session offers open, descriptor-check, and terminate
— `usage` runs the declared extractor against it, `latency` bounds
p50/p95; certification traffic is never paid, settled, or receipted),
and frozen per plan 0043 §3.4;
`GET /admin/v1/runners` shows each capability's per-offer state with the
disagreeing field named. Paid work is dispatched only to runners that are
eligible for the offer it was sold under.

The offer's `capacity.max_in_flight` is enforced before dispatch, per
eligible runner — capacity is operator-owned, so a runner never declares
its own. For long-lived remote-runner sessions, the capacity slot is held
until session finalization. `capacity.queue_limit` is rendered for policy
visibility and future queueing, but v1 dispatch currently fail-fasts when
no eligible capacity is available.

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
                         # (see that file's "RUNNING THIS FILE" header for
                         #  the env, key file and payment daemon it expects)
make help                # show all targets
```

No host Go install required.

## Configuration

A single declarative YAML file: [`examples/host-config.example.yaml`](./examples/host-config.example.yaml).
The example starts with minimal `paid-job/v1` offers for smoke bring-up and
keeps more involved shipped shapes commented out until you have runners to
attach behind them.

For OpenAI-compatible offerings, use the base capability family in
`offers[].capability` (`openai:chat-completions`, `openai:embeddings`, etc.)
and put model identity in `extra.openai.model`. Deprecated suffixed forms
such as `openai:chat-completions:<model>` are rejected by config validation.

Current broker validation for `openai:*` offerings requires:

- `extra.openai.model`
- `extra.provider`

Optional stable enrichment fields are:

- `served_model_name`
- `backend_model`
- `features.*` (booleans only)

These now come from the runner, not from the broker probing it.

The broker used to poll workload-specific endpoints — `GET /v1/models`,
`/openai-audio-speech/options`, `/v1/video/transcode/abr/presets` and
others — to hydrate `extra` on published offerings, which meant the
broker carried hardcoded knowledge of every workload's discovery
contract. A runner now declares its own identity and extensions in its
attach document (`runner-attach.md` §3.2), the first certified runner
freezes them into the offer, and the adapter profile in the agent is
where a new workload's facts are described. That polling, its
`--metadata-refresh-interval` flag, its `metadata` block on
`GET /registry/health`, and its `livepeer_metadata_refresh_*` metrics are
removed (plan 0043 item 11).
`GET /registry/health` reports one entry per advertised offer per eligible
runner. Its JSON contract is unchanged — the roster, the registry daemon's
live-health layer and the chain probe all read it — but nothing is polled to
produce it: certification says whether a runner can serve the offer and the
attach tunnel says whether it is reachable, and both are read live on every
request, so there is no interval in which the answer is stale.
When `pool_snapshot.url` is configured,
each backend may also include a `pool` object with snapshot freshness
(`fresh`, `stale`, `expired`, `bootstrap_pending`, or `fetch_error`), cached
timestamps, and whether the latest snapshot contained an entry for that
backend+offering tuple. In that mode, `selection_eligible`,
`selection_weight`, and `selection_reason` describe the broker's final
Pool-aware routing decision, not just the runner's local eligibility. If every
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
│   ├── backend/        # outbound forwarding over a runner's connection
│   ├── offers/         # offer engine: match, certify, freeze, eligibility
│   ├── certification/  # certification step engine
│   ├── runnerattach/   # attach-document parsing + validation
│   ├── runners/        # attached-runner registry
│   ├── credentialstore/# sealed attach credentials
│   ├── health/         # health vocabulary + aggregation (no prober)
│   ├── selection/      # runner eligibility + weighting
│   ├── workerconn/     # runner QUIC / WebSocket sessions
│   ├── payment/        # payment-daemon client (mock for dev)
│   └── observability/  # metrics, logging, request-id
├── examples/
│   ├── host-config.example.yaml         # annotated reference
│   └── host-config.offers.example.yaml  # compact quick-start
├── docs/
└── Dockerfile / Makefile / go.mod
```

`internal/` packages reflect what's shipped.
