# Tech debt tracker (openai-gateway)

| Item | Opened | Notes |
|---|---|---|
| Inlined Livepeer client instead of `@tztcloud/livepeer-gateway-middleware` | 2026-05-06 | v0.1 inlines the client logic in `src/livepeer/` to avoid cross-package npm/Docker plumbing. Switch to the package via npm workspaces or a file: dep + multi-stage Docker once that infrastructure is in place. The inlined code mirrors the package's API; the swap should be mechanical. |
| Audio model read from `Livepeer-Model` header instead of multipart parsing | 2026-05-06 | A real production gateway would parse the multipart stream to read the `model` form-field. v0.1 takes a shortcut: caller passes `Livepeer-Model: <model>` as a header. The route still forwards the original multipart body verbatim. |
| `@grpc/grpc-js` + `@grpc/proto-loader` runtime deps | 2026-05-06 | Plan 0014 dropped the hand-rolled TS encoder and now calls the local payer-daemon over a unix-socket gRPC connection. The daemon is the canonical envelope encoder. Trade: the gateway is no longer zero-runtime-deps. Acceptable because once warm-key handling lands (plan 0017), gateway-side encoding would be a key-handling surface anyway. |
| Resolver policy is intentionally simple | 2026-05-08 | Manifest-driven routing now uses `service-registry-daemon`, but policy is still v1: hard `constraints`, soft `extra` preference, lowest-price tie-break, and best-effort retry to the next candidate. Add broker-health polling / backoff weighting / latency scoring later if needed. |
| `NODE_VERSION` Dockerfile ARG pinned to 22 | 2026-05-06 | Tracks repo core belief #16. |
