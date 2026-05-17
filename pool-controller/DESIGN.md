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
5. Expose explicit receipt-write APIs so later broker/accounting integrations
   have a durable, idempotent storage boundary.
6. Expose a manual round-close API that derives member payouts from included
   final work receipts, so a future `pool-reconciler` or other round-close
   producer can trigger canonical round closure without embedding payout math
   itself.
7. Persist deterministic payout intents derived from closed round receipts so
   accounting can advance to an auditable operator-export step before any real
   payout execution worker exists.
8. Track payout intent lifecycle transitions inside `pool-controller` so later
   execution workers or manual operator loops can update accounting state
   without mutating the round-close source of truth.
9. Preserve executor-facing settlement references on payout intents so a future
   payout worker can write back batch IDs, tx hashes, or similar external
   handles without changing the accounting contract.
10. Add bounded lease semantics for exported payout intents so multiple payout
    executors can coordinate batch pickup without double-submitting the same
    payout set.
11. Support lease renewal and explicit release so executor restarts and fatal
    preflight failures can hand work forward without waiting for TTL expiry.
12. Expose derived payout anomaly reporting so operators can see stale
    `submitted`, long-lived `failed`, and near-expiry `leased` intents without
    reconstructing state from raw records.
13. Support explicit failed-intent requeue so alert-driven payout retry can
    return work to the executor queue without mutating closed-round inputs.
14. Expose round-level payout summaries so operators can reason about payout
    completion state at the accounting-round level instead of per intent only.
15. Persist explicit payout `failed_at` timestamps so failure-age reporting and
    future retry policy can key off canonical state instead of inferred timing.
16. Persist canonical retry history (`retry_count`, `last_requeued_at`) on
    payout intents so future retry budgets and policy can live in controller
    accounting state rather than executor-local heuristics.
17. Expose retry-history-based payout alerts so operators can see likely retry
    exhaustion or immediate re-failure patterns before any unattended retry
    policy exists.
18. Surface retry churn at the round-summary layer so accounting operators can
    identify problematic payout rounds without drilling into raw intent rows.
19. Surface retry churn at the member-summary layer so operators can identify
    problematic payout recipients without drilling into raw intent rows.

The generator is intentionally deterministic so later services can regenerate
config on member approval/drain events and compare diffs safely.
