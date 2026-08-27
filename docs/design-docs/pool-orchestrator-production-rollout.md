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

- `pool-controller` owns offers, member onboarding, assignments, and desired
  broker runtime
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

### 4.3 Create orch-owned offers

Use `pool-controller` admin UI/API to create the canonical offer catalog.

Do not let member capability claims define public offerings.

### 4.4 Onboard members

Normal control-plane sequence:

1. member submits join request
2. operator refreshes verification if needed
3. operator approves or rejects
4. operator assigns approved backend(s) to orch-owned offer(s)

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
- approved members are present
- expected assignments are active

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

### 7.3 Member approved but unassigned

This is not a publication failure by itself. It is expected staging state.

Action:

- either assign the backend to an active offer
- or leave it intentionally unpublished

Playbook:

1. inspect `GET /admin/v1/assignment-candidates`
2. inspect backend verification status and claim-to-offer suggestions
3. either:
   - create an assignment and apply broker runtime
   - or leave the backend unassigned intentionally
4. do not expect coordinator-visible inventory change until assignment + apply
   have both completed

### 7.4 Join request rejected or verification failed

This is an onboarding review outcome, not a runtime failure.

Playbook:

1. inspect join preview and backend verification error details
2. confirm whether the failure is:
   - endpoint reachability
   - probe configuration
   - incompatible claim shape
   - operator policy rejection
3. communicate the reason back to the member/operator workflow
4. refresh verification only after the underlying backend or claim issue is
   corrected

### 7.5 Member suspended or backend disabled after publication

This is an active routing change and requires runtime reconciliation.

Playbook:

1. change member/backend status in `pool-controller`
2. confirm assignment and candidate state reflect the change
3. the change pushes automatically; to cut a host off immediately,
   revoke its credential — that deletes the secret and closes its
   connections
4. verify broker convergence
5. confirm broker `/registry/health` and coordinator view reflect the new
   routable set
6. if the change was emergency containment, leave the member/backend suspended
   until a new verification cycle completes

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
