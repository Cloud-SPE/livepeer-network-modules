# Tech debt tracker (gateway-adapters)

Component-local debt. Append rows when you encounter debt; remove them when
the debt is paid down in the same PR.

| Item | Opened | Notes |
|---|---|---|
| ws-realtime / rtmp-ingress / session-control middleware not implemented | 2026-05-06 | v0.1 narrowed scope per plan 0008. Each requires a follow-up plan. ws-realtime needs a WebSocket client (node `WebSocket` global is Node 22+, otherwise `ws` package); rtmp/session are session-open-shape helpers (similar to the broker side). |
| `NODE_VERSION` Dockerfile ARG pinned to 22 | 2026-05-06 | Tracks repo core belief #16. Bump to current LTS when convenient. |
