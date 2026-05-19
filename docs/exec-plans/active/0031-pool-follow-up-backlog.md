---
plan: 0031
title: Pool follow-up backlog
status: active
phase: backlog
opened: 2026-05-18
owner: harness
related:
  - "completed plan 0029 — pool node design"
  - "completed plan 0030 — pool backend scoring and broker-integrated selection for OpenAI workloads"
  - "completed plan 0033 — pool control plane onboarding and offer-assignment reset"
  - "completed plan 0032 — pool live RTMP contract decision"
---

# Plan 0031 — Pool follow-up backlog

## 1. Purpose

Pool routing/scoring for the first OpenAI-focused slice is now implemented in
code. This backlog captures the remaining Pool work that is still open after
the `0030` slice, grouped into:

- near-term next implementation slices
- explicit `0029` non-implemented items
- deferred work already acknowledged by `0029` / `0030`

This is a prioritization document, not a new architecture reset.

## 1.1 Current disposition

The items in this backlog are intentionally **incomplete** as of 2026-05-18 and
explicitly **deferred** from the current implementation workstream. Do not
assume these slices are partially supported unless a later numbered plan or
completed-status doc says otherwise.

## 2. Current baseline

The following are already in place:

- `pool-controller` backend-selection state, scoring, synthetic probes,
  summaries, overrides, and metrics
- `capability-broker` Pool snapshot polling, Pool-aware selection, health
  surfaces, and request-time selection metrics
- `orch-coordinator` compatibility with additive broker health fields such as
  `backends[]`
- first-slice synthetic probes for:
  - `openai:chat-completions`
  - `openai:embeddings`
  - `openai:audio-*` via explicit probes plus interaction-mode fallbacks
  - `video:transcode.abr` via ABR preset-surface validation

The remaining tasks below should be read as follow-up work, not missing core
behavior from the shipped first slice.

## 3. Priority order

### P0 — close out first-slice plan hygiene

These are low-risk cleanup items that reduce ambiguity around what has shipped.

1. ~~Update `0030` status from `phase: design` once the team agrees it has moved
   into shipped implementation territory.~~ **Done** — 0030 is `phase: shipped`.
2. ~~Update `0029` status text so the Pool-scoring portions no longer read like
   pending design when they are already implemented.~~ **Done** — 0029 status
   already reads "completed — the Pool node architecture from this plan is now
   implemented…".
3. ~~Add a short cross-reference from `0029` to `0031` so future readers know
   where remaining Pool work is tracked.~~ **Done** — 0031 cross-references
   0029, 0030, 0032, 0033 in frontmatter; 0029 links back to 0031 as the
   follow-up backlog.

P0 is closed. Remaining work begins at P1.

### P1 — next capability-family expansion

These are the most direct functional extensions to the shipped Pool scorer.

1. Add synthetic probes for `video:live.rtmp`.
2. Add synthetic probes for deferred session/media families only after the
   video probe recipes stabilize:
   - `vtuber`
   - `daydream`
   - other session-control workloads

`video:live.rtmp` has an architectural blocker, not just a missing probe
recipe. The shipped live RTMP path is broker-local `ffmpeg-subprocess` plus
broker RTMP/HLS listeners, while the Pool member model from `0029` is
"backend runtime only, no member-side broker." `0032` now makes that
limitation explicit and rejects Pool live RTMP offerings in config until a new
contract exists. Before a Pool synthetic probe can exist for this family, the
repo needs a concrete Pool member contract for remote live capacity resale,
for example:

- allow/require a member-side broker for live workloads
- define a remote live runner transport that the Pool broker can drive
- or explicitly defer Pool support for `video:live.rtmp`

Why this is first:

- it extends the existing scoring model without changing the control-plane
  architecture
- the codepaths for synthetic probing, snapshot export, broker consumption, and
  observability already exist
- it closes the biggest remaining functional gap between OpenAI-first Pool
  support and broader Pool support

Status: incomplete and deferred.

### P2 — operator workflow and policy automation

These items are explicitly called out as not implemented yet in `0029`, but
they are the next meaningful Pool product surface after routing quality.

The control-plane reset for this group now has its own concrete implementation
plan in [`0033-pool-control-plane-onboarding-and-assignment.md`](./0033-pool-control-plane-onboarding-and-assignment.md).

1. Member self-service portal / wallet sign-in UX. **Deferred.**
2. ~~Automated member approval workflow.~~ **Shipped.** Config-gated via
   `policy.auto_approve_join_requests`; the policy worker auto-approves any
   pending JoinRequest the admission-review preview already considers
   Approvable. Implementation lives in
   `pool-controller/internal/service/autoapprove`.
3. ~~Policy-driven auto-drain / auto-suspend orchestration.~~ **Shipped (drain).**
   Config-gated via `policy.auto_drain_backends`,
   `policy.backend_failure_rate_threshold`, and `policy.backend_min_samples`;
   the policy worker drains any active backend whose worst per-offering
   recent failure rate exceeds the threshold. Implementation lives in
   `pool-controller/internal/service/autodrain`. Auto-suspend (member-level)
   is still deferred.
4. Multi-listener split between admin/member/public binaries if the current
   single-process surface becomes an operational constraint. **Deferred.**

Recommended order inside this group:

1. ~~approval workflow~~
2. ~~policy-driven auto-drain / suspend~~ (drain done; member-level
   suspend still open)
3. member self-service UX
4. binary/listener split

Reason:

- approval and policy automation affect actual Pool operations
- UX can follow once the approval/policy state model is stable
- binary/listener split is mostly deployment hardening, not product behavior

Status: items 2 and 3 (auto-drain portion) shipped; items 1 and 4 still
deferred.

### P3 — payout and accounting follow-up

The current accounting path is usable, but there is still larger Pool economics
work left if payout automation becomes the next bottleneck.

1. Harden reconciler/executor operational runbooks and dashboards around lease
   churn, retries, and payout failure pressure.
2. Decide whether the current admin-plane payout flow is sufficient for the
   expected operator scale.
3. If not, plan the next accounting milestone explicitly:
   - stronger payout orchestration
   - more automated retry/suspend coupling
   - eventual `PoolPayout` smart-contract path

Status: incomplete and deferred.

### P4 — deferred research / protocol-adjacent work

These remain deferred unless priorities change:

1. Online sampling / shadow-backend response diffing.
2. Fully automatic member self-service approval.
3. Member-set pricing.
4. HA / clustered `pool-controller`.
5. Manifest / resolver / gateway protocol changes for Pool-aware routing.
6. Force-include override.
7. Emergency degraded fallback routing mode.

Status: incomplete and deferred.

## 4. Recommended next slice

If the goal is to continue Pool implementation immediately, the recommended
next slice is:

1. resolve the Pool contract for remote `video:live.rtmp` beyond the explicit
   `0032` defer/validation
2. add `video:live.rtmp` synthetic probe support
3. only then move to `vtuber` / `daydream` / session-control probing

That keeps the next work aligned with the architecture already in place and
extends Pool scoring to the next most important workload families without
reopening routing contracts.

## 5. Exit criteria for this backlog

This backlog can be retired or split once either of these is true:

1. the next concrete implementation slice gets promoted into its own numbered
   plan, or
2. `0029` and `0030` are both moved to completed status and the remaining Pool
   work is small enough to live in the tech-debt tracker instead of a dedicated
   backlog plan.
