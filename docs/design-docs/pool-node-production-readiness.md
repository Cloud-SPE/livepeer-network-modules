# Pool Node Production Readiness

This checklist is the operator-facing production gate for plan 0029. It is
cross-cutting: it binds `pool-controller`, `pool-reconciler`,
`pool-payout-executor`, `capability-broker`, `payment-daemon`, and
`protocol-daemon`.

Since plan 0044 the gate is wider than the payout path it started as. A pool
that onboards members without an operator touch is a pool that can also
*mis*-place a workload, promote a bad host, or pay out without a person, all
without an operator touch. Sections 7–9 below are that surface: they gate the
catalog, the listener split, and how far payout automation is allowed to go.
The design those sections check is described in
[`pool-overlay-flows.md`](./pool-overlay-flows.md).

## Current implementation status

The [regional-pool design](regional-pools.md) is the current requirements
contract. Local implementation and validation are tracked by `lnm-l17`;
regional acceptance and live rollout remain open. The older single-pool
Arbitrum dust-payout evidence does not validate this two-region topology.

Implemented locally:

- Independent immutable pool identities, versioned regional terms, and
  dedicated payout-wallet bindings with durable payout intents.
- Complete source-qualified revenue and finalized billed-work collection,
  Model B window accounting, exact member rounding, separate commission/dust,
  zero-work operator allocation, immutable approvals and correction holds.
- Scheduled round/window progression, hardware relay, placement and ladder
  evaluation; these are no longer uncalled library helpers.
- Shared durable GPU ownership, generation fencing, broker drain/revocation,
  actual agent stop acknowledgement and explicit regional transfer.
- One durable portal wallet session with locally verified regional tokens,
  explicit joins, private member reports and qualified aggregate telemetry.
- Fleet-aware agent bundles, durable credential-pair rotation, runner
  companions and persistent model caches. Initial audio/chat templates now
  contain startup definitions; image availability is not hardware readiness.
- Scoped HTTPS APIs, local keyless protocol observers, generated deployment
  configurations, persistent volume inventory and manual backup/restore tools.

Validation evidence and limits live in
[`infra/scenarios/regional-pools`](../../infra/scenarios/regional-pools/README.md),
including [runner readiness](../../infra/scenarios/regional-pools/runner-readiness.md)
and [backup/manual restore](../../infra/scenarios/regional-pools/backup-and-restore.md).
Full regional acceptance remains `lnm-l17.6`; the coordinated major-4 release
and independent review remain `lnm-rqz`. No regional deployment, funds movement
or image publication is authorized by the implementation task.

Payout approval defaults to human review. The existing graduation procedure in
[`pool-controller/RUNBOOK.md`](../../pool-controller/RUNBOOK.md) still governs
any later move to bounded automatic approval. Recovery uses preserved ledgers
and explicit operator fencing; no automatic failover or recovery UI is added.

## Release checklist

### 1. Topology and persistence

Use the generated [regional topology](../../infra/scenarios/regional-pools/README.md):
one controller, reconciler, executor and keyless observer per region; EU
transcode and US transcode/audio/LLM brokers with independent receiver ledgers;
shared portal and durable ownership authority. Run one writer for each store.

Validate the generated Compose and component configurations offline before
activation. Persist every volume in the deployment manifest, plus member-agent
credential and desired-state directories. Follow the coordinated cold-backup
and manually fenced restore procedure; backing up only the three pool stores
loses broker/receiver obligations and ownership state. Preserve immutable pool
IDs, source identities, payout-wallet bindings and all submitted intents.

Catalogs, terms, source registrations, credentials, TLS material and payout
policy are part of the protected deployment/backup inventory. Separate member,
admin and reporting HTTPS origins and verify actual proxy route boundaries.

### 2. Secrets and wallet operations

- Store the executor keystore outside git and outside image layers.
- Deliver the keystore password through the deployment secret system, not a
  checked-in file.
- Define hot-wallet funding policy:
  - target balance floor
  - refill owner
  - refill process
- Gas and regional-wallet funding are operator costs. They never reduce a
  member allocation. Use distinct EU/US payout wallets, separate from payee
  and receiver signing identities; preserve the executor binding on restart.
- Verify role-, pool- and source-scoped service credentials, pinned TLS trust,
  expiry and reload/revocation behavior. A VPN does not replace API auth.

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
  - ladder runs erroring (`ladder run error` on stderr) — a ladder that cannot
    run is a pool frozen at whatever it was routing
  - members stuck in `probationary` past a full window, which usually means
    they are not getting the traffic the promotion criterion needs
  - enrolled GPUs with no placement, which usually means no enabled template
    matches them
- Scrape and retain the component metrics surfaces where available.

### 5. Privacy and reporting

The accepted regional design permits aggregate pool telemetry and a member's
own share, revenue and payout proof. It does not permit listing other members'
private earnings or enrollment details. Verify wallet ownership on every member
route and pool scope on every regional authorization token. The shared portal
must not hold controller-admin credentials.

Regional/aggregate reports must retain pool, chain, asset, interval, source
coverage and freshness. Combine only compatible reports; an unavailable region
is stale/missing, never a successful zero. A complete zero-work window is a
different state and allocates its revenue to the operator under Model B.

### 6. Runtime validation

These live steps require separate deployment and funds authorization. Local
synthetic-chain/process tests do not satisfy them.

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

### 7. Workload catalog and placement

The catalog is the pool's product. It is deployed as files, so it needs the
same care as any other artifact that changes what the pool sells.

- Decide how `template_catalog_dir` is delivered to the running controller —
  baked into the image, or a mounted volume — and how a change is reviewed.
  A template edit changes prices, capacity, certification, and which GPUs a
  workload lands on; it should not be a live edit on a host.
- Confirm the catalog loads clean at boot. A malformed template is a hard
  error by design: a silently skipped one leaves members running nothing with
  no explanation. A *missing* directory is not an error — an accounting-only
  controller legitimately has no catalog.
- Confirm every enabled primary and companion image is pinned and pullable
  from a member host. Validate model loading, runner self-description and
  certification on each actual GPU class, including co-resident workloads.
  See the regional runner-readiness record; unused catalog entries can still
  lack startup definitions and must not be silently enabled.
- Set `price` overrides deliberately. `price_default` in each template is a
  starting point derived from a dated market reference, not a rate card.
- Review `GET /admin/v1/placement-plan` against real enrolled hardware before
  applying it, and read the reason codes on GPUs that get no placement.
- Confirm shared ownership rejects concurrent regional claims. A transfer must
  drain all source brokers, revoke old credentials, receive actual stop proof
  and advance the generation before the target starts. Neither an outage nor
  elapsed time authorizes takeover. Preserve the audited duplicate-claim
  exception path without bypassing the regional ownership authority.

### 8. Listener split and member surface

- Regional production uses split listeners (`listen.member` set) and scoped
  HTTPS origins. Verify at the proxy that the member address
  answers no `/admin/*` route and exposes only the intended member/reporting
  routes. Test it, do not reason about it.
- Verify member session hardening end to end: single-use nonce with TTL,
  per-address rate limit, cookie expiry and rotation, CSRF on mutating forms,
  login-attempt limits, and that signing out invalidates the session
  server-side rather than only dropping the cookie.
- Confirm enrollment bearer tokens are limited to agent routes, while locally
  verified portal tokens bind wallet, pool, session, scope and expiry. Test
  cross-wallet access, wrong-region replay, revoked trust and terms acceptance
  independently in both regions.
- Confirm member actions land in the audit log — opt-out, credential rotation,
  host retirement.
- Confirm host retirement drains before it stops: the placement is marked
  draining, the broker stops dispatching, and in-flight work finishes.

### 9. Ladder and payout automation

- Set the `ladder:` block deliberately, or accept the 0040 §8.3 defaults, and
  record which you chose: `probation_share_ppm`, `probation_max_in_flight`,
  `probation_min_jobs`, `exploration_ppm`, `score_floor`,
  `recertify_after_failures`, `active_share_cap_ppm`,
  `evaluation_interval_ms`. A zero field means "not configured" and takes the
  default — it does not mean zero, which for a probation share would starve
  the very evidence promotion needs.
- Set `placement.max_templates_per_class` if this pool's stacking stance
  differs from the built-in one.
- Watch one member climb `certified → probationary → active` on staging and
  confirm the audit trail shows only automatic transitions with reason codes
  and evidence.
- Confirm the operator exception queue (`GET /admin/v1/exceptions`) surfaces
  suspensions and duplicate-GPU claims, and that lifting a suspension is
  audited with its actor.
- Start payouts at `payouts.policy_path` unset, or with `shadow: true`. Do not
  enable `auto_approve` before completing phase 0 of the graduation plan in
  [`pool-controller/RUNBOOK.md`](../../pool-controller/RUNBOOK.md).
- Verify the pause file (`payouts.pause_path`) actually stops automatic
  approval, before you need it to.
- Verify the policy hash is recorded on every decision, so an audit can prove
  which rules were in force.

## Recommended production defaults

- Run one instance per region of:
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
- Use separate dedicated Arbitrum payout wallets for EU and US.
- Run split listeners (`listen.member` set), even if both sit behind one
  proxy.
- Start with `payouts.policy_path` unset. Automatic approval is something a
  pool graduates into, not a default it inherits.
- Enable a small catalog. Every enabled template is an offer the pool must be
  able to certify, route, and pay for.

## Exit criteria for “production ready”

This Pool node stack can be called production ready when:

- all regional, broker, receiver, shared ownership and portal stores persist,
- secrets and keystore handling are documented and deployed safely,
- alerting exists for payout and round-close failure modes,
- at least one real staging payout has succeeded,
- repeated multi-round soak runs are clean,
- retry/privacy/wallet policies are explicit rather than implicit.

Onboarding adds its own bar, because zero-touch means a mistake also lands
without a touch:

- every enabled template carries a pinned, pullable `runner_compose.image`,
  and a real member host has started it,
- a new member has reached `active` on staging with **zero** operator actions,
  and that member's audit trail shows only automatic transitions,
- a template reassignment has reached a member host with no member action, and
  the withdrawn service drained rather than dropping in-flight work,
- the member listener has been tested — not reasoned about — to expose no
  `/admin/*` route and no other member's private figures,
- the operator exception queue is the only place operator gestures appear.
