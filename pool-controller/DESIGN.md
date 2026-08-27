# pool-controller design

> **Scope note.** The numbered list below is the plan-0029 implementation
> record. The accounting half (items 10–24 — receipts, round close, payout
> intents, leases, alerts) is still what the controller does. The member half
> has been replaced twice since: plan 0040 turned members into connected hosts
> that attach outbound, and plan 0044 §5 phase A deleted the join-request →
> verify → approve → assign model outright — `JoinRequest`, `MemberBackend`,
> the legacy `Assignment`, admission review, backend verification, and the
> config compatibility loader are all gone. Items below are reconciled with
> that; where you need the current picture rather than the history, read
> [`../docs/design-docs/pool-overlay-flows.md`](../docs/design-docs/pool-overlay-flows.md)
> and
> [`../docs/exec-plans/active/0044-zero-touch-pool-onboarding.md`](../docs/exec-plans/active/0044-zero-touch-pool-onboarding.md).

Initial implementation scope for plan 0029:

1. Hold the pool's member records: a member (wallet address), the hosts that
   member enrolled, the GPUs those hosts reported, and the pool templates
   placed on those GPUs. Members create themselves by wallet sign-in; there is
   no operator-authored `members[].backends[].offerings[]` config any more, and
   no compatibility loader for it.
2. Validate that each published `(capability_id, offering_id)` tuple is unique
   at the published manifest layer while allowing repeated runner candidates.
3. Push the Pool's offer set and the credentials that may attach to the
   Pool broker over its admin API (`PUT /admin/v1/offers`,
   `PUT /admin/v1/credentials`). The controller sends only operator-owned
   facts — offering id, capability, protocol, match selector, price,
   capacity, metadata, certification. Transports, work unit, extractor,
   endpoint paths and readiness are not the controller's to send: member
   hosts attach outbound and declare those themselves, and the broker
   freezes the first certified runner's shape into the offer (plan 0043).
4. Persist startup/reload snapshots of the active Pool config in BoltDB so
   operator state survives process restarts. There is no rendered broker
   config to snapshot — see item 3.
5. Persist backend-selection state records keyed by
   `(member, backend, capability_id, offering_id)` so later Pool scoring,
   probe, and outcome-ingest work has a durable state boundary. The key
   survived the deletion of the `MemberBackend` registry it once joined
   against; scoring is being re-expressed against template assignments and
   the broker's attached runners (plan 0044 §3.5).
6. Expose a read-only admin snapshot of persisted backend-selection state so
   future broker pollers can integrate against a stable controller surface.
7. Expose operator override endpoints for quarantine, drain, warm-up, and
   max-share-cap updates against persisted backend-selection state.
8. Expose a conservative backend-outcome ingest API that updates persisted
   backend-selection records with real-traffic timestamps and persisted
   rolling-window / EMA-backed scoring updates.
9. Add an opt-in synthetic probe scaffold that discovers in-scope
   OpenAI offerings from config, executes concrete chat/embeddings probes plus
   partial audio-family probes, explicitly skips unsupported audio subtypes,
   and writes synthetic-confidence observations back into persisted
   backend-selection state.
10. Expose explicit receipt-write APIs so later broker/accounting integrations
   have a durable, idempotent storage boundary.
11. Expose a manual round-close API that derives member payouts from included
   final work receipts, so a future `pool-reconciler` or other round-close
   producer can trigger canonical round closure without embedding payout math
   itself.
12. Persist deterministic payout intents derived from closed round receipts so
   accounting can advance to an auditable operator-export step before any real
   payout execution worker exists.
13. Track payout intent lifecycle transitions inside `pool-controller` so later
   execution workers or manual operator loops can update accounting state
   without mutating the round-close source of truth.
14. Preserve executor-facing settlement references on payout intents so a future
   payout worker can write back batch IDs, tx hashes, or similar external
   handles without changing the accounting contract.
15. Add bounded lease semantics for exported payout intents so multiple payout
    executors can coordinate batch pickup without double-submitting the same
    payout set.
16. Support lease renewal and explicit release so executor restarts and fatal
    preflight failures can hand work forward without waiting for TTL expiry.
17. Expose derived payout anomaly reporting so operators can see stale
    `submitted`, long-lived `failed`, and near-expiry `leased` intents without
    reconstructing state from raw records.
18. Support explicit failed-intent requeue so alert-driven payout retry can
    return work to the executor queue without mutating closed-round inputs.
19. Expose round-level payout summaries so operators can reason about payout
    completion state at the accounting-round level instead of per intent only.
20. Persist explicit payout `failed_at` timestamps so failure-age reporting and
    future retry policy can key off canonical state instead of inferred timing.
21. Persist canonical retry history (`retry_count`, `last_requeued_at`) on
    payout intents so future retry budgets and policy can live in controller
    accounting state rather than executor-local heuristics.
22. Expose retry-history-based payout alerts so operators can see likely retry
    exhaustion or immediate re-failure patterns before any unattended retry
    policy exists.
23. Surface retry churn at the round-summary layer so accounting operators can
    identify problematic payout rounds without drilling into raw intent rows.
24. Surface retry churn at the member-summary layer so operators can identify
    problematic payout recipients without drilling into raw intent rows.

The offer push is intentionally deterministic and idempotent so it can be
re-sent on any pool-state change and diffed safely. It once keyed off member
approval and backend drain events; those gestures no longer exist, so the
triggers are now offer edits, template placement, certification results, and
host revocation.

Current scoring implementation notes:

- Pool cooldown uses a persisted 5-minute rolling window of repeated
  `backend_failure` outcomes.
- Synthetic probes already update persisted confidence and exclusion state, but
  they do not yet participate in a longer-horizon EMA memory model.
- `real_success_score` is now recomputed from a 5-minute rolling window of
  `success` and `backend_failure` outcomes, then blended with EMA memory.
- `real_latency_score` is now recomputed from a 5-minute rolling window of
  latency observations using a normalized p95-style signal, then blended with
  EMA memory.
- EMA memory now uses a 24-hour half-life and drifts toward neutral `0.5`
  between observations.
- First-probe success and recovery from synthetic/cooldown exclusion now
  re-enter through a warm-up-capped weight.
- Warm-up now auto-graduates after enough recent routed samples.
- Manual warm-up override is now separate from automatic warm-up recovery so
  operator policy can be cleared independently of runtime recovery state.
