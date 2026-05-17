# pool-reconciler design

Current implementation scope:

1. Define the typed config needed to reach `pool-controller`.
2. Add a read-only `protocol-daemon` client so this component can consume round
   timing without owning chain watchers.
3. Add a read-only `payment-daemon` client so realized round revenue can come
   from confirmed redemptions rather than inferred balances.
4. Define the canonical round-close request shape consumed by
   `pool-controller /admin/v1/round-close`.
5. Ship a manual submit command that reads a request JSON file and posts it to
   `pool-controller`.
6. Guard manual submit with a round-source preflight when
   `protocol_daemon_socket` is configured, so operators cannot accidentally
   close the current or a future round.
7. Persist round-close attempt state locally so the reconciler can avoid
   re-closing already closed rounds.
8. On startup, backfill a bounded number of missed completed rounds before
   resuming the live round-event stream.
9. While running, retry pending failed rounds on a local ticker using the
   persisted attempt timestamps in the state store.

The current round-close producer contract is:

1. `protocol-daemon` provides round timing.
2. `payment-daemon` provides confirmed round revenue.
3. `pool-controller` provides persisted final work receipts by `round_id`.
4. `pool-reconciler` computes the canonical close payload and submits it back
   to `pool-controller`.

The reconciler's BoltDB state is intentionally narrow: it stores per-round
attempt counts, last error, and closed checkpoints. It is a local job-runner
checkpoint, not a source of accounting truth.
