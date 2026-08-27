# pool-controller runbook

## Role

`pool-controller` is the Pool accounting and admin source of truth. It is not
in the request path, but it is the source of record for:

- orch-owned offers
- join requests
- approved members and backends
- backend-to-offer assignments
- desired broker runtime
- work receipts
- round receipts
- payout intents
- payout retry history
- lease state

It is also the operator control plane for broker runtime convergence when the
Pool broker state is **pushed**, not rendered. The controller sends its
offer set and the credentials that may attach over the broker admin API
(`PUT /admin/v1/offers`, `PUT /admin/v1/credentials`) whenever pool state
changes. There is no rendered broker config file, no staging command, and
no reload: runners tell the broker what they are, and the broker freezes
those facts into the offer (plan 0043).

## Production topology

The Pool production shape spans two sides:

- public/data-plane host:
  - `pool-controller`
  - `capability-broker`
  - `orch-coordinator`
  - `payment-daemon` receiver
  - `pool-reconciler`
  - `pool-payout-executor`
- secure-orch / protocol host:
  - `protocol-daemon`
  - `service-registry-daemon`
  - `secure-orch-console`

`pool-controller` does not replace the secure-orch sign cycle. The normal
publication flow remains:

1. `pool-controller` manages offers, members, assignments, and broker apply
2. `capability-broker` advertises the resulting inventory
3. `orch-coordinator` scrapes broker offerings/health and builds the candidate
4. secure-orch signs
5. `orch-coordinator` publishes the signed manifest

## Required production inputs

Bootstrap config:

- `identity.orch_eth_address`
- durable `--data-dir`
- `admin_auth.bearer_token_ref: env://...`

Broker apply integration:

- `bootstrap.broker_apply_command`:
  stages the rendered broker YAML where the broker host expects it
- `bootstrap.broker_apply_timeout_ms`
- `bootstrap.broker_admin_url`
- `bootstrap.broker_admin_auth`
- `bootstrap.broker_admin_timeout_ms`

Broker private admin surface:

- `capability-broker` `admin_auth.method: bearer`
- `capability-broker` `admin_auth.secret_ref: env://...`
- private reachability from `pool-controller` to:
  - `POST /admin/v1/runtime/reload`
  - `GET /admin/v1/runtime`

Secure-orch side:

- separate running `protocol-daemon`
- separate running `secure-orch-console`
- cold orch signing key only on secure-orch

## Production bring-up order

1. Bring up secure-orch/protocol host first.
2. Bring up `pool-controller` with durable storage and admin auth.
3. Create orch-owned offers in `pool-controller`.
4. Accept member join requests and verify backends.
5. Create assignments from approved backends to orch-owned offers.
6. Confirm the push landed: the recorded revision carries `push_error`
   when the broker did not accept it, and `changed_offers` / `revoked_hosts`
   when it did. Runner and certification state is read from the broker —
   see the coordinator's Runners, Offers and Certification pages.

## Required runtime inputs

- a Pool config YAML
- a durable `--data-dir`
- admin bearer auth:
  - `admin_auth.bearer_token_ref: env://...` in production
  - `admin_auth.bearer_token` is acceptable only for local testing

Important boundary:

- the supported production config is bootstrap-only
- legacy nested `members[].backends[].offerings[]` config is migration-only and
  should not be used as the steady-state operator workflow

## Start

```bash
go run ./cmd/livepeer-pool-controller serve \
  --config ./examples/pool-controller-config.example.yaml \
  --data-dir ./var/pool-controller \
  --listen :8080
```

Compose:

```bash
docker compose -f compose/docker-compose.yml up -d
```

The normal operator path is persisted state plus the admin/member APIs. Legacy
nested `members:` config is no longer a supported runtime or migration surface
for `pool-controller`.

## Primary operator workflow

### 1. Offer and member control plane

The normal operator sequence is:

1. create/update offers
2. review join requests
3. refresh backend verification if needed
4. approve or reject the member
5. assign approved backends to active offers

Useful admin reads:

- `GET /admin/v1/offers`
- `GET /admin/v1/join-requests`
- `GET /admin/v1/members`
- `GET /admin/v1/member-backends`
- `GET /admin/v1/assignments`

### 2. Broker runtime convergence

The normal production action is:


That flow now means:

1. `pool-controller` renders the desired broker YAML
2. optional apply command stages the file
3. `pool-controller` triggers broker reload
4. broker reports a broker-local reload `attempt_id`
5. `pool-controller` confirms:
   - broker reload attempt matches the triggered `attempt_id`
   - broker `loaded_revision == desired_revision`

Do not treat shell-command exit alone as proof of convergence.

Primary runtime reads:


Broker-side corroboration:

- broker `GET /admin/v1/runtime`

The manual runtime endpoints remain fallback/debug controls only:


Use them only when the operator intentionally needs to bypass the normal
broker-admin apply path for investigation or break-glass handling.

### 2.1 Apply-command deployment patterns

Choose one explicit staging pattern for `bootstrap.broker_apply_command`.

#### Same-host file replace

Use when broker and controller share the same host filesystem contract.

Example:

```bash
install -m 0644 "$POOL_CONTROLLER_BROKER_CONFIG_PATH" /etc/livepeer/host-config.yaml
```

#### Shared-volume container staging

Use when broker and controller are separate containers sharing a writable
volume.

Example:

```bash
install -m 0644 "$POOL_CONTROLLER_BROKER_CONFIG_PATH" /shared/broker/host-config.yaml
```

#### External wrapper

Use when a checked-in wrapper script handles staging into the broker’s real
config path.

Regardless of pattern, success still requires broker-confirmed:

- `last_reload_attempt_id`
- `loaded_revision`

## Health checks

- `GET /healthz`
- `GET /readyz`
- `GET /metrics` on `listen.metrics` (default `:9090`)

## High-value admin reads

- `GET /admin/v1/state`
- `GET /admin/v1/snapshots`
- `GET /admin/v1/payout-intents`
- `GET /admin/v1/member-payouts`
- `GET /admin/v1/payout-rounds`
- `GET /admin/v1/payout-alerts`
- `GET /admin/v1/audit-events`

## Metrics

Scrape the dedicated metrics listener, not the admin port.

High-value Pool routing metrics now include:

- backend selection counts by `state`
- grouped `routing_reason` and `exclusion_reason` counts
- automatic warm-up and cooldown counts
- average effective selection score by offering
- average recent-window age by offering
- the live scorer settings currently applied after defaults and reload
- backend outcome ingest counts by outcome class
- synthetic probe run totals and durations
- per-capability synthetic probe result counts by `status` and `reason`
- persisted work-receipt counts by `status`
- persisted payout-intent counts by `status`
- persisted payout-intent retry pressure: `livepeer_pool_payout_intent_retry_count_max`,
  `livepeer_pool_payout_intent_with_retries_total`
- oldest unresolved payout failure age:
  `livepeer_pool_payout_intent_failed_age_seconds_max`
- receipt-write action counters across `work` and `round` flows
- payout-intent action counters across derive/export/claim/renew/release/requeue/status updates

## Detecting payout retry churn and failure pressure

Three Prometheus gauges expose payout-execution health beyond raw status counts.
Operators should alert on the third (failed-age) and watch the first two as
leading signals.

| Symptom on dashboard | Likely cause | First step |
|---|---|---|
| `livepeer_pool_payout_intent_with_retries_total` sustained > 5% of total intents | Executor hitting transient RPC / gas errors and requeuing | Check `pool-payout-executor` logs for transaction-failure reasons; tune `max_retries` / `requeue_cooldown_seconds` if storms keep repeating |
| `livepeer_pool_payout_intent_retry_count_max` ≥ executor `max_retries` | A specific intent has exhausted retries and likely sits in `failed` | `GET /admin/v1/payout-intents?status=failed` and inspect `failure_reason`; decide between manual requeue and member suspend |
| `livepeer_pool_payout_intent_failed_age_seconds_max` > 3600 (1h) | An intent has been in a failure state for too long without operator action | Same as above; the gauge stays > 0 until status moves out of `failed` / `stale_failed` / `requeue_failed` / `lease_expired` |
| All three rising together | Executor lease churn or a systemic on-chain issue (e.g. gas spike) | Suspend the executor's batch loop, inspect the chain, then resume |

Useful admin reads while triaging:

- `GET /admin/v1/payout-intents?status=failed&since=…` — failed intents with
  reasons and retry counts
- `GET /admin/v1/payout-alerts` — controller-derived alert summaries (see
  `pool-payout-executor` RUNBOOK for response playbooks)
- `GET /admin/v1/payout-rounds?with_alerts=true` — round-level failure pressure

## Recovery notes

- `pool-controller` restarts are safe if `--data-dir` is persisted.
- If `pool-controller` is down, previously loaded broker config remains in the
  broker process; traffic can continue.
- Do not delete the BoltDB state unless you intentionally want to discard
  payout and receipt history.

Broker push failure triage:

1. The recorded runtime revision carries `push_error` when the last push
   was refused. The broker names the offer and the field it rejected, so
   the message is usually the fix.
2. Broker `GET /admin/v1/offers` — confirm which offers the broker holds
   and whether each is frozen and advertised. An offer with no certified
   runner is deliberately not advertised.
3. Broker `GET /admin/v1/runners` — confirm the hosts attached and why a
   capability is ineligible. The disagreeing field is named there.
4. Broker `GET /admin/v1/certification` — confirm what a runner proved.

The coordinator's Runners, Offers and Certification pages present all
three over the same API; use them before curling.

A push that fails leaves the broker serving what it last accepted, which
is safe: paid traffic keeps flowing to already-eligible runners and the
signed manifest is unaffected.

If broker reload fails but the prior broker runtime is still serving traffic,
prefer fix-forward and re-apply over manual state edits.

Common operator playbooks:

- desired revision drifted during apply:
  - inspect runtime history plus recent audit events
  - identify the mutating offer/member/assignment change
  - re-apply only after the desired revision stabilizes
- approved but unassigned backend:
  - inspect `GET /admin/v1/assignment-candidates`
  - create assignment if the backend should publish
  - then run broker apply
- verification or join-request rejection:
  - inspect join preview and backend verification error details
  - correct endpoint/probe/claim issues before retrying approval
- suspended member or disabled backend:
  - treat it as a routing-state change
  - apply broker runtime again before expecting broker/coordinator visibility to
    match

## Backup scope

Back up the entire `--data-dir`. That store contains the canonical Pool-side
receipt, control-plane, and payout accounting history.
