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

That means the remaining work on the payout path is mostly operations, policy
hardening, and longer-running validation rather than missing component
plumbing.

Built under plan 0044, not yet validated in production:

- the file-backed workload catalog (`templates/`) and its `{enabled, price,
  extra}` overrides; an enabled, priced template is pushed to every broker in
  `bootstrap.brokers` as a derived offer
- the placement engine (GPU class → eligible templates → primary/secondary,
  every decision carrying a reason code)
- the desired-state contract and the agent loop that acts on it, including
  drain-before-stop via the `draining` flag in `runner-attach` 1.1.0-draft
- the trust ladder, running on a 60s timer inside `pool-controller`
- the member/admin listener split and the member API
- `payout-policy.json` with shadow mode and bounded auto-approve

Known gaps to hold against a production date:

- **The shipped catalog cannot start a runner.** None of the five templates
  carries a `runner_compose` block — the v1 images and model ids are still open
  (`lnm-v12`) — so the rendered compose service has no `image`. A pool going to
  production must supply `runner_compose.image` on the templates it enables and
  validate `compose up` on a real member host.
- **Automatic window close is not on a loop.** `settlement.EvaluateClose`
  (hold-on-anomaly, hold-on-short-scale) is implemented and tested, and
  `payouts.auto_close_windows` / `payouts.scale_tolerance` exist in config, but
  nothing reads them; closing a window is still
  `POST /admin/v1/settlement-windows/close`.
- **Hardware relay is not on a loop.** `brokerpush.RelayHardware` reads the
  broker's runner view, but no scheduler or route invokes it, and the agent no
  longer posts hardware itself. Confirm how GPU inventory actually reaches
  `pool-controller` in your deployment before relying on placement.
- **The bundle's `.env` does not match the variables the agent reads.** The
  generated bundle writes `POOL_BROKER_URL`, `POOL_BROKER_QUIC_ADDR` and
  `POOL_BROKER_SESSION_CREDENTIAL`; `pool-member-agent` reads
  `LIVEPEER_BROKER_URL`, `LIVEPEER_BROKER_QUIC_ADDR` and
  `LIVEPEER_ATTACH_CREDENTIAL_FILE`. Verify a freshly downloaded bundle
  actually attaches before onboarding a real member.
- **The member portal pages are not served yet** (`lnm-6at.12`), nor is the
  rebuilt operator console (`lnm-6at.16`). The APIs behind both exist.

Recovery/runbook status:

- control-plane, broker-apply, and publish recovery playbooks now live in
  `docs/design-docs/pool-orchestrator-production-rollout.md`
- payout/reconciler recovery playbooks now also live there for:
  - stalled round close
  - stale submitted payouts
  - failed payout accumulation
  - stuck or near-expiry leases
- the four-phase plan for graduating from human payout approval to automatic
  approval — with each phase's exit criterion and kill switch — is in
  `pool-controller/RUNBOOK.md` under "Graduating to automatic payouts"

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
- Decide how the read-only inputs reach the controller host:
  - `template_catalog_dir` — the workload catalog, read at boot
  - `payouts.policy_path` and `payouts.pause_path`, if automation is enabled
  These are policy, not state: they belong in the deployment artifact and its
  review process, not in the BoltDB backup.
- Decide the listener addresses: `listen.paid` (console + `/admin/*` +
  `/public/v1/*`), `listen.member` (portal + `/member/v1/*`), `listen.metrics`.
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
  - ladder runs erroring (`ladder run error` on stderr) — a ladder that cannot
    run is a pool frozen at whatever it was routing
  - members stuck in `probationary` past a full window, which usually means
    they are not getting the traffic the promotion criterion needs
  - enrolled GPUs with no placement, which usually means no enabled template
    matches them
- Scrape and retain the component metrics surfaces where available.

### 5. Privacy and product policy

- Decide whether `GET /public/v1/member-payouts` remains public in production.
  A member can now read their own earnings authenticated, through
  `GET /member/v1/enrollments/{id}/earnings`, so the unauthenticated route is a
  convenience rather than the only way in — which makes closing it a smaller
  decision than it was.
- If it stays, note that it is registered on the admin/paid listener, not the
  member one: the member listener deliberately serves no cross-member figure.
- Decide whether any members require manual payout holds or exclusions.
- Confirm no member-visible surface reports a share of a pool total. A share
  plus a public total is another member's income by subtraction.

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
- Add `runner_compose.image` to every template you enable, and confirm the
  image tag is pinned and pullable from a member host. **The repo catalog ships
  none**, so this is mandatory before a member can run anything.
- Set `price` overrides deliberately. `price_default` in each template is a
  starting point derived from a dated market reference, not a rate card.
- Review `GET /admin/v1/placement-plan` against real enrolled hardware before
  applying it, and read the reason codes on GPUs that get no placement.
- Confirm the cross-address GPU UUID block behaves as expected: the same GPU
  UUID under two ETH addresses must not activate without an operator override,
  and the override must land in the audit log with its reason.

### 8. Listener split and member surface

- Decide whether this deployment runs split listeners (`listen.member` set) or
  single-address. Split is the production shape; single-address is supported
  and is what dev runs.
- If split: verify at the proxy, not just in config, that the member address
  answers no `/admin/*` route and serves no `/public/v1/*` cross-member
  aggregate. Test it, do not reason about it.
- Verify member session hardening end to end: single-use nonce with TTL,
  per-address rate limit, cookie expiry and rotation, CSRF on mutating forms,
  login-attempt limits.
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
- Run split listeners (`listen.member` set), even if both sit behind one
  proxy.
- Start with `payouts.policy_path` unset. Automatic approval is something a
  pool graduates into, not a default it inherits.
- Enable a small catalog. Every enabled template is an offer the pool must be
  able to certify, route, and pay for.

## Exit criteria for “production ready”

This Pool node stack can be called production ready when:

- the three Pool components run from durable persisted state,
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
  `/admin/*` route and no cross-member figure,
- the operator exception queue is the only place operator gestures appear.
