# pool-controller design

Initial implementation scope for plan 0029:

1. Parse Pool operator config describing members, their backends, and declared
   capability offerings.
2. Validate that each published `(capability_id, offering_id)` tuple is unique
   at the published manifest layer while allowing repeated backend candidates,
   and ensure each member/backend record is structurally complete.
3. Generate a broker `host-config.yaml` with one `capabilities[]` entry per
   backend candidate, carrying through transport, URL, auth, pricing, health,
   and metadata.
4. Persist startup/reload snapshots of the active Pool config and rendered
   broker config in BoltDB so operator state survives process restarts.
5. Persist backend-selection state records keyed by
   `(member, backend, capability_id, offering_id)` so later Pool scoring,
   probe, and outcome-ingest work has a durable state boundary.
6. Expose a read-only admin snapshot of persisted backend-selection state so
   future broker pollers can integrate against a stable controller surface.
7. Expose operator override endpoints for quarantine, drain, warm-up, and
   max-share-cap updates against persisted backend-selection state.
8. Expose a conservative backend-outcome ingest API that updates persisted
   backend-selection records with real-traffic timestamps and persisted
   rolling-window / EMA-backed scoring updates.
9. Add an opt-in synthetic probe runner scaffold that discovers in-scope
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

The generator is intentionally deterministic so later services can regenerate
config on member approval/drain events and compare diffs safely.

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
