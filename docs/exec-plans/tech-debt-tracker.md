# Tech debt tracker

Known debt in this repo. When you encounter debt, append a row. When you pay it down,
remove the row in the same PR.

| Item | Opened | Notes |
|---|---|---|
| Retire duplicate legacy `payer_daemon.proto` in `proto-contracts/` | 2026-05-22 | The canonical proto lives at `livepeer-network-protocol/proto/livepeer/payments/v1/payer_daemon.proto`. The duplicate at `proto-contracts/livepeer/payments/v1/payer_daemon.proto` is still imported by `pool-reconciler/internal/paymentdaemon/client.go` and two tests in the same package. Migrate those callers, then delete the `proto-contracts/livepeer/payments/v1/` copy of `payer_daemon.proto`, `types.proto`, and their generated bindings. Phase 6 leftover from active plan `0034`. |
