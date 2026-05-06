# Tech debt tracker (payment-daemon)

| Item | Opened | Notes |
|---|---|---|
| Stub ticket validation | 2026-05-06 | Any non-empty `Ticket` is accepted. Real probabilistic-micropayment validation (signature checks, ticket-params binding, redemption) is the chain-integration follow-up. |
| Sender-side gRPC surface deferred | 2026-05-06 | v0.1 only ships the receiver. Gateways encode envelopes locally. A standalone sender-side service belongs here once warm-key handling lands. |
| Interim-debit cadence not exercised | 2026-05-06 | The `Debit` RPC supports multiple calls per session; v0.1 callers issue exactly one. Long-running modes (ws-realtime / rtmp / session) will use ticker-driven interim debits in their own follow-up. |
| Warm-key handling absent | 2026-05-06 | The daemon stores no key material. The cold-key signed manifest + warm-key escalation flow lands with chain integration. |
| BoltDB record format is JSON | 2026-05-06 | Chosen for debuggability under low volume. If write throughput becomes a constraint, swap to a binary codec; the migration is a one-time pass over the bucket. |
