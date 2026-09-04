# pool-payout-executor design

Initial implementation scope for plan 0029:

1. Define typed config for reaching `pool-controller`.
2. Add a read client for payout intents filtered by status / round / member.
3. Add a write client for payout intent status updates and executor metadata.
4. Expose CLI commands that let operators or future automation prepare batches
   and report `submitted`, `paid`, or `failed`.
5. Use native `ETH` on Arbitrum as the v1 payout rail.
6. Sign transfers from a dedicated executor hot wallet and confirm them back
   into `pool-controller`.
7. Provide one-shot and looped reconciliation entrypoints that confirm
   `submitted` intents before dispatching the next `exported` batch.
8. Persist local run history and per-intent retry metadata so unattended
   executor loops recover operator context across restarts.
9. Apply local exponential backoff in unattended reconcile flows using that
   persisted retry metadata, while preserving direct manual commands as
   override surfaces.
10. Claim exported payout intents through `pool-controller` leases before
    broadcasting so multiple executor instances do not race on the same batch.
11. Resume still-valid leases already owned by the current executor after
    restart before claiming fresh exported work.
12. Explicitly release claimed leases on fatal preflight abort paths before
    any submission begins, so work returns to the exported pool immediately.
13. Release untouched leased remainder after partial submission so only the
    actually attempted subset stays pinned to the active batch state.
14. Expose an explicit failed-intent requeue command so operators can return
    failed payouts to the exported queue without coupling unattended reconcile
    loops to an implicit retry policy.
15. Drive every payout transaction through chain-commons's durable
    transaction intents (plan 0048 stage 4): one intent per controller
    intent id, so a re-run never pays twice; the processor owns the hot
    wallet's nonce, gas-bump replacement of stalled transactions, and
    reorg-aware confirmation. A submitted payout this executor has no
    record of is adopted from the controller's `tx_hash` + `external_ref`
    on the next confirm pass, which is how in-flight payouts survive an
    upgrade.
15. Expose a read-only payout-alert view through executor config/auth so
    operator automation can inspect controller-derived anomalies without
    separate curl wiring.
16. Consume explicit controller `failed_at` timing so future retry policy can
    evolve from canonical accounting state rather than inferred timestamps.
17. Expose an explicit command that requeues only controller-flagged stale
    failed payouts, so operators can act on alert policy without enabling
    unattended auto-retry.
18. Consume controller-owned retry history (`retry_count`,
    `last_requeued_at`) so future retry budgets and stop conditions can be
    defined from canonical accounting state.
19. Surface controller retry-risk alerts through executor read APIs so ops can
    inspect the same retry-limit and recent-requeue signals without separate
    controller tooling.
20. Support a default-off v1 auto-requeue policy in unattended reconcile flows:
    `max_retries=3`, `requeue_cooldown_seconds=3600`, and only transient
    failure reasons are eligible for automatic requeue.
21. Support local keystore-file signing (`keystore.json` + password file) as
    the preferred operator path for live-chain testing and runtime execution.

This component is intentionally narrow even with signing enabled: it only
executes native-ETH payouts against intents already approved by
`pool-controller`. Multi-asset or non-EVM payout rails remain future work.
