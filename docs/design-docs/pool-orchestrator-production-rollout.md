# Pool Orchestrator Production Rollout

Cross-cutting operator rollout guide for the Pool-based orchestrator shape.
This binds:

- `pool-controller`
- `capability-broker`
- `orch-coordinator`
- secure-orch sign/publish

Use this as the top-level production sequence. Component-local runbooks remain
authoritative for component-specific flags and troubleshooting.

## 1. Scope

This guide covers the production path for a Pool-based orch where:

- `pool-controller` owns the pool's policy: which workload templates are
  enabled at what price, which template lands on which member GPU (a
  *placement*), how a placement earns its share of traffic, and the money
- `capability-broker` serves the paid data path and exposes the broker-private
  runtime reload surface
- `orch-coordinator` scrapes the broker and publishes the signed manifest
- secure-orch remains the only signer

It does not replace:

- `pool-controller/RUNBOOK.md`
- `capability-broker/docs/operator-runbook.md`
- `orch-coordinator/docs/operator-runbook.md`
- `secure-orch-console/docs/operator-runbook.md`

Those are the detailed component references.

## 2. Host topology

Production is split across two trust zones.

### 2.1 Public/data-plane host

Runs:

- `pool-controller`
- `capability-broker`
- `payment-daemon` receiver
- `orch-coordinator`
- `pool-reconciler`
- `pool-payout-executor`

Public exposure:

- broker public paid/API listener
- coordinator public manifest listener

Private-only surfaces:

- `pool-controller` admin API/UI
- broker private runtime admin API
- metrics listeners

### 2.2 Secure-orch / protocol host

Runs:

- `protocol-daemon`
- `service-registry-daemon`
- `secure-orch-console`

Rules:

- cold orch key stays only on this host
- no public inbound internet
- operator-driven sign cycle remains mandatory

## 3. Required artifacts and secrets

### 3.1 Pool control plane

- durable `pool-controller --data-dir`
- `pool-controller` bootstrap config with:
  - `identity.orch_eth_address`
  - `admin_auth.bearer_token_ref`
  - `bootstrap.broker_admin_url`
  - `bootstrap.broker_admin_auth`

### 3.2 Broker

- production `host-config.yaml`
- broker `admin_auth` bearer secret via `env://...`
- private bind/reachability for:
  - `POST /admin/v1/runtime/reload`
  - `GET /admin/v1/runtime`

### 3.3 Coordinator

- `coordinator-config.yaml` with:
  - `identity.orch_eth_address`
  - broker `base_url`

### 3.4 Wallets

- cold orch signing key on secure-orch only
- hot payment receiver wallet on broker side
- hot payout wallet for `pool-payout-executor`

### 3.5 Secure-orch

- `protocol-daemon` socket available to the public/data-plane side where
  required by reconciler flow
- `secure-orch-console` reachable to the operator over a private path

## 4. Bring-up sequence

### 4.1 Secure-orch first

Bring up:

- `protocol-daemon`
- `service-registry-daemon`
- `secure-orch-console`

Do not proceed until:

- secure-orch console is healthy
- protocol daemon is reachable where reconciler expects it

### 4.2 Public/data-plane base

Bring up:

- `pool-controller`
- `payment-daemon` receiver
- `capability-broker`
- `orch-coordinator`
- `pool-reconciler`
- `pool-payout-executor`

Do not expose live traffic yet.

### 4.3 Enable the workload templates this pool sells

The workload catalog is a directory of YAML files (`template_catalog_dir`,
repo-root `templates/`), read at boot. There is no offer catalog to author: an
offer is *derived* from an enabled, priced template and pushed to every broker
in the fleet.

1. `GET /admin/v1/template-catalog` — what this build loaded
2. `PUT /admin/v1/template-overrides/{id}` — enable it and set its price. The
   `price_default` in a template file is a starting point from a dated market
   reference, not a rate card.
3. `GET /admin/v1/offers` — what those enabled templates derive into

Do not let member capability claims define public offerings. A member's runner
declares what it *is*; the pool decides what it *sells*.

> Each of the five templates in the repo catalog ships without a
> `runner_compose` block — the v1 images and model ids are still open
> (`lnm-v12`). Enabling one makes the pool advertise it, but the compose
> service rendered for a member host has no `image` and nothing will start.
> Supply `runner_compose.image` on the templates you enable.

### 4.4 Onboard members

Members onboard themselves. There is no join request and no approval gate: the
pool never dials a member-supplied endpoint, so there is nothing for the
operator to verify before admission. Trust is established later, by the broker,
from what the member's runners actually prove under certification.

Normal control-plane sequence:

1. member signs in with their wallet and enrols a host
   (`POST /member/v1/enrollments`), then runs the returned bundle — which
   contains the agent and nothing else
2. the host's GPUs reach the controller, and placement policy matches them to
   enabled templates: highest `priority` among the templates whose
   `requirements` the card satisfies and the member has not opted out of takes
   the primary slot, with a secondary only where the template names that GPU
   class and the class's stance allows a rider
3. the agent pulls its desired state and starts the runners; it re-attaches
   declaring what it now serves
4. the broker certifies each runner against the offer it matched; a placement
   becomes eligible to serve only once certification passes
5. the ladder promotes it from `probationary` to `active` on its own, once a
   settlement round has closed **and** it has completed the template's
   `min_jobs` with no serious failure

No operator gesture appears anywhere in that sequence. The operator's touches
are the exceptions: lifting a suspension, overriding a duplicate GPU UUID
claim, banning or retiring a member, and approving payout batches until those
graduate.

Two things to know before relying on it:

- **Applying a placement plan is a call, not a loop.** Review
  `GET /admin/v1/placement-plan` — it carries a reason code for every GPU,
  including the ones that got nothing — then commit it with
  `POST /admin/v1/placement-plan/apply`. `POST /admin/v1/template-assignments`
  remains for the cases policy cannot reach.
- **Confirm how GPU inventory actually reaches your controller.** The agent no
  longer posts hardware itself, and `brokerpush.RelayHardware` — which reads it
  from the broker's runner view — is implemented but not yet called by any
  loop or route. `POST /member/v1/enrollments/{id}/hardware` still exists.
  Check `GET /admin/v1/hardware-units` on a real enrolment before assuming
  placement has anything to work with.

### 4.5 Broker convergence

The controller pushes its offers and credentials to the broker over the
admin API whenever pool state changes; there is no rendered config file,
no staging command, and no reload (plan 0043).

Normal production action: none. The push happens on state change.

Required reads to confirm convergence:

- controller: the recorded runtime revision — `push_error` when the
  broker refused (it names the offer and field), `changed_offers` and
  `revoked_hosts` when it accepted
- broker `GET /admin/v1/offers` — which offers are frozen and advertised
- broker `GET /admin/v1/runners` — who is attached and, for a capability
  not serving, the disagreeing field
- broker `GET /admin/v1/certification` — what each runner proved

The coordinator's console presents all three over the same API.

A failed push leaves the broker serving what it last accepted: paid
traffic and the signed manifest are unaffected. Do not treat an absent
`push_error` on a stale revision as convergence — check the broker's own
view.

### 4.6 Refresh coordinator state

Once the broker has the intended runtime loaded:

1. verify broker `/registry/offerings`
2. verify broker `/registry/health`
3. confirm `orch-coordinator` sees the expected broker inventory

### 4.7 Sign and publish

Normal publication sequence:

1. coordinator builds the candidate
2. operator transfers candidate to secure-orch
3. secure-orch console signs
4. operator uploads signed manifest back to coordinator
5. coordinator publishes `/.well-known/livepeer-registry.json`

## 5. Success checks

Before live traffic:

### 5.1 Control-plane checks

- expected offers exist in `pool-controller`
- expected members and host enrolments are present
- each expected GPU appears as a hardware unit with a template assignment
- each template assignment has a passing certification run

### 5.2 Broker convergence checks

- controller: the recorded runtime revision has no `push_error`
- broker `GET /admin/v1/offers`:
  - every enabled offer is `frozen` and `advertised`
  - `runners.eligible > 0` on each
- broker `GET /admin/v1/runners`: expected hosts `connected`, each
  capability `accepted` at attach

### 5.3 Coordinator checks

- coordinator candidate contains expected tuples
- published manifest serves from the public listener

### 5.4 Secure-orch checks

- secure-orch diff/sign cycle completes
- published manifest sequence advances as expected

## 6. First production smoke

Run a low-risk real smoke after publish:

1. route one real gateway request through the published broker path
2. confirm broker request handling
3. confirm work receipt persisted in `pool-controller`
4. confirm round-close / payout path stays healthy afterward

Use dust-risk economics. Do not wait for a larger traffic cutover before
exercising the real production path.

## 7. Failure handling

### 7.1 Broker push failed

Check:

- the controller's recorded runtime revision — `push_error` names the
  offer and the field the broker refused
- broker `GET /admin/v1/offers`
- broker `GET /admin/v1/runners`

Focus on:

- an offer refused at validation (the message says which key)
- an offer frozen with no eligible runner — check certification
- a runner attached but ineligible — the disagreeing field is named on
  the runner view

If the broker does not hold the offers you expect:

- do not treat the rollout as converged
- do not publish a new candidate on the assumption that the new broker
  state is live
- fix forward; the next state change re-pushes

Playbook:

1. stop any publication change that depends on the refused offers
2. read `push_error` on the controller's recorded runtime revision
3. correct the offer the broker named (price, capacity, certification
   step, or a key it does not accept)
4. the next control-plane change re-pushes; there is no manual apply
5. re-check `GET /admin/v1/offers` on the broker before proceeding

### 7.2 An offer is frozen against the wrong shape

A frozen shape comes from the first runner that certified. If that runner
declared something the operator did not intend — a different model, a
different quantization — the offer is advertising it.

- inspect `GET /admin/v1/offers`: `frozen.projection` is what is
  advertised, `frozen_by` is the runner that set it
- `candidates[]` lists certified runners whose declaration disagrees,
  with the diff
- accept the intended shape from the coordinator's **Offers** page
  (`POST /admin/v1/offers/{id}/accept-shape`), then sign the candidate —
  the signature is the acceptance
- runners on the old shape become ineligible at that moment

### 7.3 Enrolled GPU running nothing

This is not a publication failure by itself. It is expected staging state: a
reported GPU serves nothing until a template is placed on it and that placement
certifies.

Placement is deterministic policy, so "running nothing" always has a reason —
read it rather than guessing.

Playbook:

1. `GET /admin/v1/hardware-units` — confirm the GPU reached the controller at
   all, and see what state the unit is in
2. `GET /admin/v1/placement-plan` — the reason code for this GPU. The common
   answers, in rough order of frequency:
   - `not_enabled` at the top of the response: no template is switched on
   - the card's driver string did not normalise to a pool class — laptop and
     Max-Q parts deliberately get no class rather than the wrong one
   - no enabled template's `requirements` match (class or VRAM floor)
   - the member opted out
   - it lost the primary slot and no template names its class as a secondary
3. `POST /admin/v1/placement-plan/apply` if the plan is right and simply has
   not been committed
4. `GET /admin/v1/template-assignments` — confirm the placement exists, and
   whether the agent has actually started it
5. `GET /admin/v1/certification-runs` — a placement only becomes eligible once
   its certification passes
6. check the template has a `runner_compose.image`. Without one the rendered
   compose service has no image and nothing can start on the member host
7. or leave the GPU deliberately unplaced
8. do not expect coordinator-visible inventory change until a placement has
   certified and the offer push has landed

### 7.4 Certification failed

This is a runner-capability outcome, not an onboarding review outcome. Nothing
on the controller side needs re-approving.

Playbook:

1. read the failed run's checks
2. corroborate with broker `GET /admin/v1/runners`, which names the capability
   field the broker disagreed with, and broker `GET /admin/v1/certification`
3. confirm whether the failure is:
   - runner not ready (image, model download, GPU not visible to the container)
   - a capability shape the offer does not accept
   - a latency or usage check the template requires
4. fix the runner and re-run certification for that placement. Repeated
   certification failures are also what the ladder acts on by itself: K
   consecutive failures send a placement back to recertify, and a serious
   failure suspends it. A suspension is one of the few things only an operator
   can lift.

### 7.5 Host revoked or retired after publication

This is an active routing change and requires runtime reconciliation.

Playbook:

1. revoke the host enrolment
   (`POST /admin/v1/host-enrollments/{id}/revoke`) in `pool-controller`.
   Member-level suspension has no admin route of its own yet — the
   `PoolMember.status` field is now set through
   `PATCH /admin/v1/pool-members/{address}`, and the operator exception queue
   is `GET /admin/v1/exceptions`
2. confirm the affected template assignments reflect the change
3. the change pushes automatically; to cut a host off immediately,
   revoke its credential — that deletes the secret and closes its
   connections
4. verify broker convergence
5. confirm broker `/registry/health` and coordinator view reflect the new
   routable set
6. if the change was emergency containment, leave the host revoked until the
   affected templates have re-certified

### 7.6 Secure-orch sign/publish blocked

If broker convergence is correct but publication is blocked:

- inspect coordinator candidate and audit state
- inspect secure-orch console diff/sign path
- do not weaken the cold-key boundary to work around the blockage

Playbook:

1. confirm broker convergence first
2. confirm coordinator candidate contains the intended tuples
3. inspect coordinator audit/history for candidate or upload rejection
4. inspect secure-orch console sign path and last-signed state
5. complete the normal operator transfer/sign/upload cycle
6. if publish remains blocked, resolve coordinator-side rejection before
   attempting another sign cycle

### 7.7 Round close stalled or missed

This is a downstream accounting progression failure. Traffic may still be
serving while accounting falls behind.

Playbook:

1. inspect `pool-reconciler` local state and logs
2. confirm `protocol-daemon` round source is advancing
3. confirm `payment-daemon` round revenue reads are healthy
4. confirm `pool-controller` is accepting `/admin/v1/round-close`
5. identify whether the failure is:
   - source round detection
   - revenue read failure
   - round-close submission failure
   - retry/checkpoint state stuck in failed
6. if needed, run a one-shot manual close/prepare flow from `pool-reconciler`
7. verify the closed round appears in `pool-controller` before resuming the
   unattended reconciler loop

### 7.8 Submitted payouts are stale

This means payout execution has progressed to chain submission tracking, but the
 intents are not resolving to `paid` or `failed` on schedule.

Playbook:

1. inspect `GET /admin/v1/payout-alerts`
2. inspect `GET /admin/v1/payout-intents`
3. inspect `pool-payout-executor` logs and local state summary
4. confirm the payout hot wallet still has ETH for gas
5. confirm the configured Arbitrum RPC is healthy
6. run `confirm-submitted` or `reconcile-once` manually if needed
7. only mark status manually if the on-chain reality is already known and the
   operator is deliberately repairing controller state

### 7.9 Failed payouts accumulating

This is a payout retry/policy problem, not a broker-routing problem.

Playbook:

1. inspect failure reasons and retry counts in `pool-controller`
2. separate:
   - transient chain/RPC issues
   - wallet balance issues
   - invalid recipient or payload issues
3. if failures are transient, use:
   - `requeue-failed`
   - or `requeue-alerted-failed`
4. if failures are structural, correct the underlying payout issue before any
   requeue
5. verify requeued intents return to `exported` and are picked up cleanly by
   the executor

### 7.10 Leases stuck or near expiry

This is an executor coordination issue.

Playbook:

1. inspect `GET /admin/v1/payout-alerts`
2. inspect leased intents and lease owner/expiry in `pool-controller`
3. check whether the owning executor is still healthy
4. if the executor is healthy, prefer `renew`
5. if the executor is dead or abandoned the work, release or let the lease
   expire, then re-run the executor claim flow
6. avoid starting multiple executors against the same exported set without an
   explicit lease policy

## 8. Primary references

- [`../../pool-controller/RUNBOOK.md`](../../pool-controller/RUNBOOK.md)
- [`../../capability-broker/docs/operator-runbook.md`](../../capability-broker/docs/operator-runbook.md)
- [`../../orch-coordinator/docs/operator-runbook.md`](../../orch-coordinator/docs/operator-runbook.md)
- [`../../secure-orch-console/docs/operator-runbook.md`](../../secure-orch-console/docs/operator-runbook.md)
- [`../../infra/scenarios/pool-orchestrator/README.md`](../../infra/scenarios/pool-orchestrator/README.md)
- [`./pool-node-production-readiness.md`](./pool-node-production-readiness.md)
