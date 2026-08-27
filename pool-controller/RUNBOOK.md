# pool-controller runbook

## Role

`pool-controller` is the Pool accounting and admin source of truth. It is not
in the request path, but it is the source of record for:

- which workload templates this pool has enabled, and at what price
  (the catalog itself is files, read at boot; only the overrides are state)
- pool members and their enrolled hosts
- hardware units (GPUs) reported by those hosts
- template assignments — a pool template placed on a GPU — their ladder state,
  and the certification runs that qualify them
- member per-template opt-outs
- the audit log
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

1. `pool-controller` holds the pool's policy — enabled templates, placements,
   ladder state — and pushes the derived offer set + attach credentials to
   every broker in the fleet
2. `capability-broker` advertises the resulting inventory
3. `orch-coordinator` scrapes broker offerings/health and builds the candidate
4. secure-orch signs
5. `orch-coordinator` publishes the signed manifest

## Required production inputs

Bootstrap config:

- `identity.orch_eth_address`
- durable `--data-dir`
- `admin_auth.bearer_token_ref: env://...`
- `template_catalog_dir` — the workload catalog, read at boot. Empty is valid
  for an accounting-only controller; a *malformed* template is a hard error, on
  purpose, because a silently skipped one leaves members running nothing with
  no explanation
- `listen.member` if this deployment splits the member surface onto its own
  address (recommended)

Broker admin integration (the push path — there is no rendered file and no
apply command):

- `bootstrap.broker_admin_url`
- `bootstrap.broker_admin_auth`
- `bootstrap.broker_admin_timeout_ms`
- `bootstrap.public_broker_url` / `bootstrap.public_broker_quic_addr` — where
  member hosts attach

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
3. Enable the workload templates this pool sells and price them
   (`GET /admin/v1/template-catalog`, then
   `PUT /admin/v1/template-overrides/{id}`). The offer set is derived from
   those, not authored separately.
4. Have members sign in with their wallet and enrol a host
   (`POST /member/v1/enrollments`) and run the bundle. The bundle contains
   the agent and nothing else.
5. Review `GET /admin/v1/placement-plan` and commit it with
   `POST /admin/v1/placement-plan/apply`. From there the agent pulls its own
   desired state, the broker certifies what attaches, and the ladder promotes
   it — no further operator step is on the member's path.
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
- legacy nested `members[].backends[].offerings[]` config is not supported at
  all: the compatibility loader that once ingested it has been removed

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

### 1. Templates, members, and placement

Members onboard themselves: they sign in with their wallet, enrol a host, and
run the bundle. There is no join request, no operator approval step, and no
member-supplied backend URL to verify — the pool never dials a member endpoint;
the member's runners attach to the broker.

**Policy is set once, in the catalog.** Templates are YAML files under
`template_catalog_dir`, read at boot. The only per-pool state is
`{enabled, price, extra}`, and an enabled, priced template is *derived* into an
offer and pushed to every broker in the fleet. There is no separate offer
record to keep in step.

1. `GET /admin/v1/template-catalog` — what this build loaded
2. `PUT /admin/v1/template-overrides/{id}` — enable and price it.
   `DELETE` the override to switch it off.
3. `GET /admin/v1/offers` — what those enabled templates derive into

> None of the five templates in the repo catalog carries a `runner_compose`
> block: the v1 images and model ids are still open (`lnm-v12`). Enabling one
> makes the pool advertise it, but the compose service rendered for a member
> host has no `image` and nothing will start there. Add
> `runner_compose.image` to the templates you enable.

**Placement is policy, not a gesture.** For each GPU, among enabled templates
whose `requirements` it satisfies and the member has not opted out of, the
highest `priority` takes the primary slot; another stacks only where it names
that GPU's class in `stacking.secondary_on` and the class's stance allows a
rider. Members opt *out*, never in.

- `GET /admin/v1/placement-plan` — what the policy would do, with a reason code
  on every GPU including the ones that get nothing, plus a pool-wide
  `not_enabled` list
- `POST /admin/v1/placement-plan/apply` — commit it. A placement leaving the
  plan is **drained**, not deleted
- `POST /admin/v1/template-assignments` — direct placement, for the cases
  policy cannot reach

Applying is a call rather than a loop on purpose: placement is deterministic,
so the plan is worth reading before it is committed.

**Act only on exceptions.** `GET /admin/v1/exceptions` is the queue —
suspensions and duplicate GPU UUID claims.

- `PATCH /admin/v1/pool-members/{address}` — suspend or reactivate a member.
  A suspension requires a reason (one with none is a decision nobody can review
  later, including the operator who made it) and drains that member's
  placements rather than stopping them dead
- `POST /admin/v1/host-enrollments/{id}/revoke` — revoke a host enrolment;
  that deletes the credential and closes its connections
- `GET /admin/v1/ladder/state` — where placements stand, read-only
- `POST /admin/v1/ladder/run` — run a ladder pass now rather than waiting for
  the timer. Read the state first: looking should not be acting

Useful admin reads:

- `GET /admin/v1/offers`
- `GET /admin/v1/pool-members`
- `GET /admin/v1/host-enrollments`
- `GET /admin/v1/hardware-units`
- `GET /admin/v1/template-catalog`
- `GET /admin/v1/placement-plan`
- `GET /admin/v1/template-assignments`
- `GET /admin/v1/certification-runs`
- `GET /admin/v1/exceptions`
- `GET /admin/v1/audit-events`

The console presents the same state, and is usually faster than curling:
`/admin/pool` (members, hosts, GPUs), `/admin/offers`, `/admin/placement` (the
plan with its rejections and reason codes), `/admin/ladder` (transitions with
the evidence sentence), `/admin/exceptions`, `/admin/payouts`, `/admin/audit`.

### 1.1 The trust ladder

The controller advances placements on a timer (default 60s), so promotion and
throttling are not operator actions:

```
certified ─▶ probationary ─(closed settlement round ∧ ≥N jobs)─▶ active
active ─(score below floor)─▶ throttled ─(recovers)─▶ active
any ─(K consecutive failures)─▶ recertify
any ─(serious failure)─▶ suspended ─(operator lifts)─▶ probationary
```

Promotion needs **both** halves. A job count alone can be run up in minutes by
a host about to fail; a closed round alone proves only that time passed.

Every transition writes `{state, reason_code, evidence, at}`, and the member
sees the same reason code you do — so "why am I throttled" is answered by the
record rather than by an operator writing an explanation.

Tune under `ladder:`: `probation_share_ppm`, `probation_max_in_flight`,
`probation_min_jobs`, `exploration_ppm`, `score_floor`,
`recertify_after_failures`, `active_share_cap_ppm`, `evaluation_interval_ms`.
A zero field means "not configured" and takes the default; it does not mean
zero. `ladder run error` on stderr means the pool is frozen at whatever it was
routing — alert on it.

### 1.2 Listeners

`listen.member` puts the member portal and `/member/v1/*` on their own address,
with no `/admin/*` route and no cross-member figure. Leaving it empty keeps
both surfaces on `listen.paid`, which is the supported single-address
deployment. Prefer the split in production: an operator console reachable from
the same address members use is one misconfigured proxy away from being
reachable *by* them, and an address boundary survives a proxy mistake that an
auth check does not. Test it rather than reasoning about it.

### 2. Broker convergence

There is no rendered broker config file, no staging command, and no reload
(plan 0043). The controller pushes what it owns — the derived offer set and the
credential hashes that may attach — to each broker over the broker admin API
(`PUT /admin/v1/offers`, `PUT /admin/v1/credentials`). Both are full,
idempotent replacements; a credential that disappears from a push is a revoke,
which closes that host's connections. Offers go first, so a host whose
credential was just accepted attaches into a broker that already knows what it
might serve.

**The normal production action is none.** The push happens on state change.
`brokerrender`, `runtimeservice`, the `/admin/v1/broker-runtime/*` routes,
`bootstrap.broker_apply_command` and `cmd/broker-apply` were deleted with that
path; if you are looking for them, you want the push above instead.

Confirm convergence from both sides:

- controller: the recorded runtime revision — `push_error` when the broker
  refused (it names the offer and the field), `changed_offers` and
  `revoked_hosts` when it accepted
- broker `GET /admin/v1/offers` — which offers it holds, and whether each is
  frozen and advertised. An offer with no certified runner is deliberately not
  advertised
- broker `GET /admin/v1/runners` — who is attached and, for a capability that
  is not serving, the field the broker disagreed with
- broker `GET /admin/v1/certification` — what each runner actually proved

The coordinator's Runners, Offers and Certification pages present all of these
over the same API. Do not treat an absent `push_error` on a stale revision as
convergence — check the broker's own view.

A failed push leaves the broker serving what it last accepted. That is safe:
paid traffic keeps flowing to already-eligible runners and the signed manifest
is unaffected. Prefer fix-forward over editing state by hand.

### 3. Payouts

Approval is human by default. `payout-policy.json` (`payouts.policy_path`) can
take it over within bounds it states explicitly, and every decision carries the
hash of the policy that made it.

- `GET /admin/v1/payout-policy` — the policy in force, and its hash
- `POST /admin/v1/payout-batches/{id}/policy-review` — what the policy says
  about this batch, without approving it
- `POST /admin/v1/payout-batches/{id}/approve` — the human gesture
- `payouts.pause_path` — the kill switch. Verify it works before you need it

A missing policy file is not an error: it means no automatic approval, which is
where every pool starts. Read "Graduating to automatic payouts" below before
enabling `auto_approve`.

`settlement.EvaluateClose` implements automatic window close with
hold-on-anomaly and hold-on-short-scale, and `payouts.auto_close_windows` /
`payouts.scale_tolerance` exist in config, but **nothing calls them yet**:
closing a window is still `POST /admin/v1/settlement-windows/close`.

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

## Graduating to automatic payouts

Money leaving the pool is the one action nobody can undo, so approval
starts entirely human and the pool earns its way out of that. Each phase
below has an exit criterion you can check and a way to stop.

The kill switch at every phase is the same: create the file named by
`payouts.pause_path`. Its presence refuses every automatic approval
until it is removed. It needs no deploy, no restart, and no code change,
which is the point — an operator who does not trust what automation is
doing must be able to stop it in one command.

`payouts.policy_path` points at `payout-policy.json`. It is strict:
unknown fields are rejected, `auto_approve.enabled` without
`max_batch_wei` is refused as a half-written config rather than read as
"any amount", and a file that cannot be parsed fails the read instead of
silently becoming a policy that approves nothing while looking
configured. The file's SHA-256 is recorded beside every decision it
makes, so an audit can prove which rules were in force at the time.

### Phase 0 — shadow

```json
{ "shadow": true,
  "auto_approve": { "enabled": true, "max_batch_wei": "...", "require_scale_gte": 0.99 } }
```

The policy runs and records what it WOULD have approved. It approves
nothing. Humans keep approving every batch as before.

**Exit criterion:** at least four consecutive settlement windows where
every batch the policy would have approved was also approved by a
person, and every batch a person held was also refused by the policy.
Zero divergence, in both directions. Read them from the audit trail —
`kind=payout_policy_decision` beside the human approvals.

Divergence in the direction of "the policy would have approved something
a person held" is the one that matters. Investigate it before restarting
the count; it usually means a bound is too loose or an anomaly is not
being detected.

### Phase 1 — automatic within tight bounds

Set `shadow: false` and keep the bounds well under a typical window:
`max_batch_wei` around a normal batch, `max_per_member_wei` around a
normal member's share, `max_batches_per_day` at one or two,
`require_scale_gte` at 0.99 or higher. Anything larger, anomalous, or
short still goes to a person, and that is the design rather than a
limitation.

**Exit criterion:** four more windows with no batch that later needed
reversing, and no operator intervention that the policy should have
caught.

### Phase 2 — widen

Raise the bounds to cover the ordinary case, guided by what the audit
shows about real batch sizes. Do not raise `require_scale_gte` — that
one is not a bound on size, it is the check that the pool collected what
it is about to pay out.

**Exit criterion:** the only batches still reaching a human are ones you
would want a human to see.

### Phase 3 — automatic except by exception

`auto_approve` unbounded except `require_scale_gte` and
`max_batches_per_day`. Human approval remains for held windows, which
means: attribution anomalies, and windows whose settlement scale came in
short. Those are the cases where the pool would be paying out money it
did not collect, and no bound makes them safe to automate.

**At every phase**, `max_batches_per_day` stays set. It is not a trust
measure, it is a blast radius: it bounds how much a bug can move before
someone notices.

## Recovery notes

- `pool-controller` restarts are safe if `--data-dir` is persisted. The
  template catalog and `payout-policy.json` are read at boot, so a change to
  either needs a restart (or `POST /admin/v1/reload`) to take effect.
- If `pool-controller` is down, the broker keeps serving what it last accepted;
  traffic can continue. Member hosts also keep running what they were running:
  the agent treats an unreachable controller as "no new instruction", not as a
  reason to stop.
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

Common operator playbooks:

- enrolled GPU running nothing — placement is deterministic, so there is always
  a reason; read it rather than guessing:
  - `GET /admin/v1/hardware-units` — confirm the GPU reached the controller and
    what state the unit is in
  - `GET /admin/v1/placement-plan` — the reason code for this GPU. Usually: no
    template enabled (`not_enabled`), the driver string did not normalise to a
    pool class (laptop and Max-Q parts deliberately get none), no enabled
    template's `requirements` match, the member opted out, or it lost the
    primary slot and nothing names its class as a secondary
  - `POST /admin/v1/placement-plan/apply` if the plan is right and simply has
    not been committed
  - `GET /admin/v1/template-assignments` — confirm the placement exists and the
    agent started it
  - `GET /admin/v1/certification-runs` — a placement only becomes eligible once
    its certification passes
  - check the template has a `runner_compose.image`; without one nothing can
    start on the member host
- host is running the wrong thing, or a withdrawal has not taken effect:
  - the agent polls every `POOL_POLL_EVERY` (default 30s) and reports back;
    check the reported revision against the current one
  - a withdrawn service is marked `draining` in the attach document *before*
    the container stops, so it can linger deliberately while in-flight work
    finishes
- member stuck in `probationary`:
  - promotion needs a closed settlement round **and** the template's
    `min_jobs`. A member with neither is usually not getting traffic, not
    failing
- certification failing:
  - read the failed run's checks; the broker names the capability field it
    disagreed with (broker `GET /admin/v1/runners`)
  - fix the runner (image, model, capability shape) and re-run certification —
    there is nothing to "re-approve" on the controller side
- host revoked or retired:
  - treat it as a routing-state change
  - confirm the offer push landed before expecting broker/coordinator
    visibility to match

## Backup scope

Back up the entire `--data-dir`. That store contains the canonical Pool-side
receipt, control-plane, and payout accounting history.
