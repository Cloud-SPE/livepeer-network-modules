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
  - optional `bootstrap.broker_apply_command`
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

### 4.5 Apply broker runtime

Normal production action:

1. `POST /admin/v1/broker-runtime/apply`
2. `pool-controller` stages desired broker YAML if an apply command is
   configured
3. `pool-controller` triggers broker reload
4. broker returns a broker-local reload `attempt_id`
5. `pool-controller` confirms:
   - broker `last_reload_attempt_id` matches the triggered attempt
   - broker `loaded_revision == desired_revision`

Required reads after apply:

- `GET /admin/v1/broker-runtime`
- `GET /admin/v1/broker-runtime/history`
- broker `GET /admin/v1/runtime`

Do not treat apply-command exit alone as convergence.

### 4.5.1 Broker apply deployment patterns

The operator must choose one explicit staging pattern for
`bootstrap.broker_apply_command`.

#### Pattern A — same-host file replace

Use when `pool-controller` and `capability-broker` run on the same host and the
broker loads `host-config.yaml` from a stable on-disk path.

Shape:

- broker reads a fixed path such as `/etc/livepeer/host-config.yaml`
- apply command copies `POOL_CONTROLLER_BROKER_CONFIG_PATH` to that path
- controller then calls broker `POST /admin/v1/runtime/reload`

Typical command shape:

```bash
install -m 0644 "$POOL_CONTROLLER_BROKER_CONFIG_PATH" /etc/livepeer/host-config.yaml
```

Use when:

- host-level deployment
- systemd-managed broker
- compose with bind-mounted config path

#### Pattern B — shared-volume container staging

Use when `pool-controller` and `capability-broker` run as separate containers on
the same machine and share a writable volume for broker config.

Shape:

- both containers mount the same volume
- broker reads a stable in-container path from that volume
- apply command writes the desired YAML into the shared volume path
- controller then calls broker reload

Typical command shape:

```bash
install -m 0644 "$POOL_CONTROLLER_BROKER_CONFIG_PATH" /shared/broker/host-config.yaml
```

Use when:

- docker compose public/data-plane deployment
- no host-level config-management system is in front of the containers

#### Pattern C — external config-management hook

Use when config staging is delegated to an external control system.

Shape:

- apply command is a wrapper script
- wrapper script moves the rendered YAML into the broker’s expected config
  location by whatever production mechanism the operator uses
- controller still requires broker reload + broker attempt/revision confirmation

This is acceptable only if the wrapper is deterministic and checked in as part
of the deployment system of record.

#### Not yet defined here

This guide does not yet lock a multi-node clustered rollout controller for:

- kubernetes ConfigMap rollouts
- per-site fan-out to multiple brokers from one `pool-controller`
- automatic broker fleet orchestration across regions

For now, choose an explicit single-target broker apply mechanism and document it
alongside the deployment.

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

- `GET /admin/v1/broker-runtime`:
  - `dirty=false`
  - `broker_dirty=false`
  - `broker_reload_status=applied`
- broker `GET /admin/v1/runtime`:
  - expected `loaded_revision`
  - expected `last_reload_attempt_id`

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

### 7.1 Broker apply failed

Check:

- `GET /admin/v1/broker-runtime`
- `GET /admin/v1/broker-runtime/history`
- broker `GET /admin/v1/runtime`

Focus on:

- `broker_reload_attempt_id`
- `broker_reload_status`
- `broker_reload_error`
- `broker_loaded_revision`

If the broker did not confirm the intended attempt/revision:

- do not treat the rollout as converged
- do not publish a new candidate on the assumption that the new broker state is
  live
- fix forward and re-apply

Playbook:

1. stop any publication change that depends on the failed runtime
2. confirm the desired revision in `pool-controller`
3. confirm the broker-local latest `attempt_id`, status, and loaded revision
4. verify the staged file path and contents used by `broker_apply_command`
5. correct the staging or broker config issue
6. run `POST /admin/v1/broker-runtime/apply` again
7. re-check controller and broker history before proceeding

### 7.2 Desired revision drifted during apply

If `pool-controller` reports drift during apply:

- reload state from `pool-controller`
- inspect recent offer/member/assignment mutations
- re-run apply only after the desired runtime stabilizes

Playbook:

1. fetch `GET /admin/v1/broker-runtime`
2. fetch `GET /admin/v1/broker-runtime/history`
3. inspect recent `GET /admin/v1/audit-events` for:
   - offer changes
   - member/backend status changes
   - assignment changes
4. decide whether the newest desired revision is the intended one
5. if yes, apply again against the new desired revision
6. if no, revert the accidental control-plane mutation through the normal API
   surface, then apply again

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
3. run `POST /admin/v1/broker-runtime/apply`
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

## 8. Primary references

- [`../../pool-controller/RUNBOOK.md`](../../pool-controller/RUNBOOK.md)
- [`../../capability-broker/docs/operator-runbook.md`](../../capability-broker/docs/operator-runbook.md)
- [`../../orch-coordinator/docs/operator-runbook.md`](../../orch-coordinator/docs/operator-runbook.md)
- [`../../secure-orch-console/docs/operator-runbook.md`](../../secure-orch-console/docs/operator-runbook.md)
- [`../../infra/scenarios/pool-orchestrator/README.md`](../../infra/scenarios/pool-orchestrator/README.md)
- [`./pool-node-production-readiness.md`](./pool-node-production-readiness.md)
