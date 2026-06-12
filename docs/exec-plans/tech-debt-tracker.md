# Tech debt tracker

Known debt in this repo. When you encounter debt, append a row. When you pay it down,
remove the row in the same PR.

| Item | Opened | Notes |
|---|---|---|
| Sign-agent rate-limit latch has no in-console clear gesture | 2026-06-12 | Plan 0042: a rate-limit breach pauses all auto-signing until cleared, but the only clear today is a console restart. Add an operator gesture (button on the Manifests page) that calls `policy.RateLimiter.Clear` and audits `agent_resumed`. |
| Candidate long-poll variant on orch-coordinator | 2026-06-12 | Plan 0042 §5.1 deferred a `?wait=55s` long-poll on the candidate routes. Plain conditional GET ships first; add only if 304-poll volume measured in production warrants held connections. |
| Retire duplicate legacy `payer_daemon.proto` in `proto-contracts/` | 2026-05-22 | The canonical proto lives at `livepeer-network-protocol/proto/livepeer/payments/v1/payer_daemon.proto`. The duplicate at `proto-contracts/livepeer/payments/v1/payer_daemon.proto` is still imported by `pool-reconciler/internal/paymentdaemon/client.go` and two tests in the same package. Migrate those callers, then delete the `proto-contracts/livepeer/payments/v1/` copy of `payer_daemon.proto`, `types.proto`, and their generated bindings. Phase 6 leftover from active plan `0034`. |
