# Tech debt tracker (gateway-adapters)

Component-local debt. Append rows when you encounter debt; remove them when
the debt is paid down in the same PR.

| Item | Opened | Notes |
|---|---|---|
| `NODE_VERSION` Dockerfile ARG pinned to 22 (TS half) | 2026-05-06 | Tracks repo core belief #16. Bump to current LTS when convenient. |
| `GO_VERSION` Dockerfile ARG pinned to 1.25 (Go half) | 2026-05-07 | Tracks repo core belief #16. Bump to current stable when convenient. |
| `PayerDaemon.GetSessionDebits` returns UNIMPLEMENTED on the daemon side | 2026-05-07 | Plan 0008-followup added the RPC surface so the gateway-adapter can ask for a final work-units count on session close. The daemon's actual debit-tracking ledger is plan-0015 territory; until then the server returns UNIMPLEMENTED and the adapter falls back to 0. |
