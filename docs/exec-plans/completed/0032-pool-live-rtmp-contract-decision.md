---
plan: 0032
title: Pool live RTMP contract decision
status: completed
phase: shipped
opened: 2026-05-18
owner: harness
related:
  - "completed plan 0029 — pool node design"
  - "completed plan 0030 — pool backend scoring and broker-integrated selection for OpenAI workloads"
  - "active plan 0031 — pool follow-up backlog"
---

# Plan 0032 — Pool live RTMP contract decision

## 1. Problem

`0031` identified `video:live.rtmp` as the next Pool probe-family candidate,
but the current Pool member model from `0029` is:

- member supplies a backend runtime only
- no member-side `capability-broker`
- no member-side `payment-daemon`

That works for remote HTTP backends like OpenAI runners and ABR runners. It
does not map cleanly onto the shipped `video:live.rtmp` implementation, which
is currently broker-local:

- `backend.transport: ffmpeg-subprocess`
- broker-owned RTMP ingest listener
- broker-owned HLS egress and session lifecycle

So the blocker is not “missing synthetic probe logic.” The blocker is that
there is no remote live-member contract for the Pool to target.

## 2. Decision

For the current Pool generation, `video:live.rtmp` is explicitly unsupported in
the `pool-controller` member/backend admission and assignment model.

`pool-controller` should reject any member offering that attempts to publish:

- `capability_id: video:live.rtmp`
- or `interaction_mode: rtmp-ingress-hls-egress@v0`

under the current backend-provider-only Pool model.

## 3. Rationale

- The current live RTMP implementation is not a remote HTTP backend that the
  Pool broker can forward to.
- Allowing Pool configs to advertise `video:live.rtmp` today would imply a
  support contract that the data-path implementation does not actually have.
- A fake “probe” would hide the real issue and create false confidence.
- Failing validation early is safer than silently generating broker configs for
  an unsupported Pool/live topology.

## 4. Non-decision

This plan does **not** choose the future live-member topology yet. Plausible
future directions include:

1. allow or require a member-side broker for live workloads
2. define a new remote live runner transport that the Pool broker can drive
3. design a separate Pool-live topology distinct from the current backend-only
   member model

Those are follow-up design tasks, not part of this decision.

## 5. Implementation status

Shipped (as of 2026-05-19):

- **Operator docs**: `pool-controller/README.md` documents the limitation and
  links to this plan.
- **Probe-layer behavior**: `pool-controller/internal/service/probes/probes.go`
  returns `capability_out_of_scope` for `video:live.rtmp`, so the workload is
  unsupported by the scoring path.
- **Explicit config-level rejection**: `pool-controller/internal/poolscope`
  codifies the rejected `capability_id` and `interaction_mode` strings and
  exposes `EnsureSupportedClaim`. The check is wired into:
  - `internal/service/offerservice` — admin-side offer create/update
  - `internal/server/member` — member join-request submission
  - `internal/service/admissionreview` — operator-facing join-claim preview
  Each rejection error cites this plan.
- **Tests**: `poolscope_test.go` covers the helper directly; each wired
  surface has a regression test asserting both the string match and the
  plan citation.

## 6. Exit criteria

This plan can move to completed when:

1. explicit validation rejects the two strings in member/backend/offering
   records with a clear error — **done**
2. operator docs mention the limitation — **done**
3. `0031` points at this plan — **done** (frontmatter `related:` and body)

All exit criteria met. Plan is ready to move to `completed/`.
