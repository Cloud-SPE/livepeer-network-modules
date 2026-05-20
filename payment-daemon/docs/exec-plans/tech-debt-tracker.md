# Tech debt tracker (payment-daemon)

| Item | Opened | Notes |
|---|---|---|
| Sender-side feedback loop is caller-driven | 2026-05-20 | `CreatePayment` and `ProcessPayment` are separated by the broker/caller, so sender invalidation on `INVALID_RECIPIENT_RAND` currently relies on `PayerDaemon.ReportPaymentResult`. This preserves explicit `work_id` identity, but the retry path is not fully automatic. |
| Interim-debit cadence not exercised | 2026-05-06 | The `Debit` RPC supports multiple calls per session; v0.1 callers issue exactly one. Long-running modes (ws-realtime / rtmp / session) will use ticker-driven interim debits in their own follow-up. |
| Payee admin surface is token-gated but socket-shared | 2026-05-20 | `PayeeAdmin.ResetSession` is mounted on the same unix socket as `PayeeDaemon` and gated by a bearer token. If operators need stricter separation later, split it onto a distinct socket or listener. |
| BoltDB record format is JSON | 2026-05-06 | Chosen for debuggability under low volume. If write throughput becomes a constraint, swap to a binary codec; the migration is a one-time pass over the bucket. |
