---
plan: 0044
title: Zero-touch pool onboarding
status: active
phase: planning
opened: 2026-08-26
owner: harness
related:
  - "active plan 0043 — connected runners and the offer-only manifest pipeline (epic 1; this plan builds on its §7 seam)"
  - "active plan 0040 — pool template onboarding and connected-worker reset (superseded for §3–§10 by 0043 + this plan; §4.2 GPU uniqueness and §11 settlement stand)"
  - "active plan 0031 — pool follow-up backlog (P2 portal and listener split land here)"
  - "docs/design-docs/pool-overlay-flows.md"
  - "docs/design-docs/pool-node-production-readiness.md"
audience: pool-controller / pool-member-agent / payout maintainers, operators
---

# Plan 0044 — Zero-touch pool onboarding

**Status:** planning — decisions locked 2026-08-26 (§2); no implementation
started. Epic 2 of two. Depends on epic 1 (`lnm-pkv`) for attach, credentials,
freeze, certification execution, and selection weights.

## 1. Purpose

> **A GPU owner goes from wallet to earning without an operator touching
> anything in the common case.** Sign in, run one command, done. The operator
> sets policy once — which templates are enabled at what price — and acts only
> on exceptions.

Today the member journey needs the operator twice (template assignment,
certification start), the member once more on every reassignment
(`update.sh`), has no member-facing UI, and the controller still carries the
pre-0040 model (join requests → member backends → assignments → broker-runtime
diff) alongside the 0040 model. This plan deletes the old model outright and
makes every step of the new one automatic.

## 2. Decisions (locked)

| # | Decision | Chosen |
|---|---|---|
| 1 | Target | Zero operator touch by default. Auto-assignment, auto-certification → probation → active, agent-pulled reassignment, legacy model deleted. |
| 2 | Who picks a GPU's template | **Operator policy, deterministically**: template `requirements` + `priority` + `stacking`; highest-priority eligible template is primary, secondaries per stacking policy. Members may opt *out* of templates, never opt in. First attach is hardware-only; the controller matches, the agent starts runners, the host re-attaches with capabilities. |
| 3 | Runner lifecycle on the host | **Agent-driven compose.** Bundle is the agent only; the controller computes desired state (compose fragment per template + GPU `device_ids` + model downloads); the agent pulls it, writes `runners.compose.yaml`, runs `docker compose up -d --remove-orphans`, drains before `down`, reports status. |
| 4 | The ladder | All transitions automatic. Promotion = **one full Livepeer round AND ≥ N accepted jobs** (default 20 per template) with no serious failure. Operator only: lift suspension, cross-address GPU override (audit reason), ban/retire. Caps/floors are pool config defaults. Controller owns the ladder, pushes weights/caps to the broker. |
| 5 | Member surface | **Required.** Server-rendered portal in `pool-controller` on its own member/public listener (admin split mandatory), wallet sign-in, session hardening. Ladder states carry reason codes + evidence. Operator console drops every legacy page. |
| 6 | Template = the pool's offer | One object: offer fields + placement policy + `runner_compose` + ladder/economics. **Templates are versioned YAML files in a curated repo catalog**; per-pool operator state is `{enabled, price, extra}` overrides. Separate offer catalog removed. Operator gesture: enable template + set price. |
| 7 | Payouts | Window close automatic (held on anomaly). Batch approval human by default; a `payout-policy` file allows **bounded** auto-approve (off by default), every decision audited. **A documented graduation plan to fully automatic payouts is a deliverable** (§3.7). |

## 3. Target model

### 3.1 Member journey

```
wallet sign-in ─▶ "run this command" ─▶ docker compose up (agent only)
      │                                        │
      │                              attach: credential + hardware[]
      │                                        ▼
      │                     controller: match GPUs → templates (policy)
      │                                        ▼
      │                     agent pulls desired state → runners start
      │                                        ▼
      │                     re-attach with capabilities → broker certifies
      │                                        ▼
      │                     probation (capped) ──round + N jobs──▶ active
      ▼                                        │
 portal: hosts · GPUs · template · ladder state + reason · earnings · payouts
```

No operator step appears on the path. Operator touches are exceptions:
suspension lift, cross-address GPU UUID override, ban/retire, payout batch
approval (until graduated, §3.7).

### 3.2 Template file (curated catalog)

```yaml
# templates/openai-chat-llama-3-70b.yaml
id: llama-3-70b-shared
capability: openai:chat-completions
offering_id: llama-3-70b-shared
protocol: paid-job/v1
display_name: Llama 3 70B (shared)
price_default: { amount_wei: "210000000", per_units: 1 }   # operator overrides
capacity: { max_in_flight: 4, queue_limit: 8 }
extra_from_runner: [x-quantization]
certification:
  - { name: ready,   type: readiness, required: true }
  - { name: smoke,   type: request,   required: true, config: { … } }
  - { name: usage,   type: usage,     required: true }
  - { name: latency, type: latency,   config: { samples: 3, p50_max_ms: 4000 } }
requirements: { gpu_models: [RTX 4090, RTX 5090], gpu_vram_min_bytes: 24000000000 }
priority: 10
stacking: { primary: true, secondary_on: [] }
runner_compose:
  image: ghcr.io/…/vllm-runner:1.4
  env: { MODEL: llama-3-70b }
  models: [{ name: llama-3-70b, size_bytes: … }]
probation: { share_ppm: 20000, max_in_flight: 1, min_jobs: 20 }
active: { share_cap_ppm: 150000 }
commission_bps: 1000
```

Per-pool state (`pool-controller` DB): `template_overrides[id] = {enabled,
price, extra}`. An enabled template with a price becomes an `offers[]` entry
pushed to each broker (0043 §3.1); manifest advertisement follows the freeze
rule (0043 §3.4). Initial catalog: the five 0040 §4.3 families.

### 3.3 Placement policy engine

Input: attach `hardware[]` (relayed from the broker), member opt-outs, enabled
templates. For each GPU: eligible = templates whose `requirements` match and
not opted out; primary = highest `priority`; secondaries = those whose
`stacking.secondary_on` includes this GPU's class, subject to the class's
stacking stance (0040 §4.4). Output: desired `TemplateAssignment` set; changes
become desired-state revisions the agent pulls. GPU UUID uniqueness across ETH
addresses blocks activation (0040 §4.2 unchanged).

### 3.4 Agent desired-state loop

`GET /member/v1/enrollments/{id}/desired-state` (enrollment-token auth) returns
`{revision, services[]{name, compose_fragment, device_ids, models[]}}`. Agent:
poll (and wake on a tunnel hint), merge into `runners.compose.yaml`, pull
images / download models with progress, `docker compose up -d
--remove-orphans`, report `{revision, services[]{name, status, detail}}`.
Drain: a service leaving desired state is marked draining in the attach
document before it is stopped; the broker stops dispatching to it. The Docker
socket requirement and what the pool may run on the host are stated in the
member README.

### 3.5 Ladder

```
certified ─▶ probationary ─(round ∧ ≥N jobs, no serious failure)─▶ active
active ─(score < floor / backoff pattern)─▶ throttled ─(recovers)─▶ active
any ─(shape change · image update · K consecutive failures)─▶ recertify
any ─(invalid output · repeated cert failure)─▶ suspended ─(operator)─▶ probationary
```

Every transition writes `{state, reason_code, evidence, at}`; the portal and
operator console render both. Pool config: `probation.share_ppm`,
`probation.max_in_flight`, `probation.min_jobs`, `exploration_ppm`,
`score_floor`, `recertify_after_failures`. Weights/caps push to the broker via
the epic-1 selection-weight hook.

### 3.6 Member portal and listeners

- Listeners: `member` (public: portal + `/member/v1/*`) and `admin` (operator
  console + `/admin/*`) on separate addresses; the admin mux is never mounted
  on the member listener.
- Auth: SIWE-style nonce/verify as today, hardened for browsers — single-use
  nonce with TTL and per-address rate limit, cookie session with expiry and
  rotation, CSRF token on every mutating form, login-attempt limits, member
  action audit.
- Pages: sign-in · get started (bundle + one command) · hosts (GPUs, running
  template, ladder state + reason, agent status) · earnings (running window
  attribution, payout history) · settings (per-template opt-out, rotate
  credential, retire host — with confirmation).
- Privacy: a member sees only their own data; no cross-member figures.

### 3.7 Payouts and the graduation plan

- Window close: automatic once all rounds are closed and reconciled; `held`
  when `scale < 1 − tolerance` or attribution anomalies exist.
- Approval: human by default, on a page showing scale, per-offering line
  items, anomalies.
- `payout-policy.json` (strict, fail-closed, hashed into audit, mirrors
  `sign-policy.json`): `auto_approve: {enabled, max_batch_wei,
  max_per_member_wei, require_scale_gte, max_batches_per_day}`,
  `shadow: true|false`.
- **Graduation to fully automatic payouts** (deliverable, shipped as a runbook
  section): phase 0 — shadow mode records what policy *would* approve for ≥ 4
  windows with zero divergence from human approvals; phase 1 — auto-approve
  within tight bounds; phase 2 — widen bounds per the audit; phase 3 —
  `auto_approve` unbounded except `require_scale_gte` and the daily rate
  limit, human approval only for `held` windows. Each phase has an explicit
  exit criterion and a kill switch (`pause` file, as 0042).

## 4. Component impact

| Component | Removed | Added |
|---|---|---|
| `pool-controller` | legacy model: `JoinRequest`, `MemberBackend`, `Assignment` types/repos, `admissionreview`, `autoapprove`, `assignmentpolicy` (old), `backendverify`, `compat`, `offerservice`, member `join-requests` routes, admin routes + pages for join-requests/members(legacy)/assignments/broker-runtime | template catalog loader + overrides; placement engine; desired-state endpoint; automatic ladder with reason codes; member listener + portal; payout policy + auto window close; rebuilt operator console |
| `pool-member-agent` | static `POOL_WORKER_BACKENDS`, controller hardware report | desired-state loop, compose apply, status report, drain marking, credential re-enroll |
| `capability-broker` | — | (epic 1) hardware relay, selection-weight hook, draining flag in attach |
| `pool-reconciler` / `pool-payout-executor` | — | unchanged; executor consumes policy-approved batches |
| repo | — | `templates/` curated catalog |

## 5. Work breakdown

Beads epic `lnm-6at` (children `lnm-6at.1`–`.17`). Cross-epic dependencies
on `lnm-pkv.*` are recorded in beads.

**Phase A — remove the legacy model**
1. Delete join-request / member-backend / assignment types, repos, services, routes, pages, and `compat`; re-express any auto-approval config as template policy.

**Phase B — templates**
2. Template file format + loader + validation; curated `templates/` catalog for the five 0040 families; per-pool `{enabled, price, extra}` overrides; remove `offerservice`.
3. Push enabled templates as `offers[]` to each broker over the admin API (idempotent, per-broker). *(after 2, `lnm-pkv.17`)*

**Phase C — placement**
4. Placement policy engine (requirements / priority / stacking / opt-out) over hardware relayed from the broker; cross-address GPU block. *(after 2, `lnm-pkv.7`)*
5. Desired-state endpoint with revisions; compose fragment rendering with `device_ids` and model downloads. *(after 4)*

**Phase D — agent**
6. Agent desired-state loop: poll/wake, merge, pull, `compose up`, status report, drain marking. *(after 5, `lnm-pkv.12`)*
7. Bundle shrinks to agent-only; agent-side credential re-enroll before expiry. *(after 6, `lnm-pkv.6`)*

**Phase E — ladder**
8. Automatic ladder with reason codes + evidence; promotion criterion; pool config defaults; weights/caps push. *(after `lnm-pkv.9`, `lnm-pkv.10`)*
9. Operator exception queue: suspensions, duplicate GPUs, bans/retire. *(after 8)*

**Phase F — portal**
10. Member/admin listener split; browser-grade session hardening; member action audit.
11. Member API contract: status, earnings, opt-out, rotate, retire, bundle. *(after 4, 8)*
12. Portal pages in the 0037 design system. *(after 10, 11)*

**Phase G — payouts**
13. Automatic window close with held-on-anomaly.
14. `payout-policy.json`: shadow mode, bounded auto-approve, audit, pause file. *(after 13)*
15. Graduation runbook (§3.7 phases, exit criteria, kill switches). *(after 14)*

**Phase H — operator console**
16. Rebuild console for the new model: members, hosts/GPUs, templates + overrides, exceptions, settlement, payouts, audit; delete legacy templates. *(after 2, 8)*

**Phase I — docs**
17. Update `pool-overlay-flows.md`, `pool-node-production-readiness.md`, `pool-orchestrator-production-rollout.md`; mark 0040 superseded; member README. *(after 6, 12, 14)*

## 6. Deferred

- Demand-aware placement weighting (refinement of §3.3).
- Multi-controller HA (0031 P4).
- ERC-1271 / Safe member identity (0040 §5.1 stance stands).
- Member-set pricing (0031 P4; contradicts decision 6).

## 7. Success criteria

- A new member reaches `active` on a supported GPU with **zero** operator
  actions; the audit log for that member shows only automatic transitions.
- A template reassignment reaches the member host without any member action.
- The controller contains one member model; no route, page, or type from the
  join-request model remains.
- The member listener exposes no `/admin/*` route (tested).
- A payout window closes and materializes a batch with no operator action;
  approval remains the only human gesture until graduation.
