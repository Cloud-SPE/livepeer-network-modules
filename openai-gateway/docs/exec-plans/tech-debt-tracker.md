# Tech debt tracker (openai-gateway)

| Item | Opened | Notes |
|---|---|---|
| Inlined Livepeer client instead of `@tztcloud/livepeer-gateway-middleware` | 2026-05-06 | v0.1 inlines the client logic in `src/livepeer/` to avoid cross-package npm/Docker plumbing. Switch to the package via npm workspaces or a file: dep + multi-stage Docker once that infrastructure is in place. The inlined code mirrors the package's API; the swap should be mechanical. |
| Streaming chat completions are buffered, not pass-through | 2026-05-06 | The `node:http`-based http-stream client reads the response to EOF before returning so trailers are accessible. Result: SSE bodies are delivered atomically to the OpenAI client. Format is correct; latency semantics differ. Improvement: change the http-stream client API to expose an async iterable + a trailer Promise. |
| Audio model read from `Livepeer-Model` header instead of multipart parsing | 2026-05-06 | A real production gateway would parse the multipart stream to read the `model` form-field. v0.1 takes a shortcut: caller passes `Livepeer-Model: <model>` as a header. The route still forwards the original multipart body verbatim. |
| Hand-rolled TS protobuf encoder for the Payment envelope | 2026-05-06 | `src/livepeer/payment.ts` encodes the four-field `livepeer.payments.v1.Payment` message inline rather than depending on protobufjs/google-protobuf, to keep the openai-gateway image zero-runtime-deps. Cross-checked against the Go encoder via the unit test. Migrate to a generated client whenever a `proto-ts/` module lands. |
| Hardcoded broker URL via env var | 2026-05-06 | Real resolver integration via `service-registry-daemon` is gateway-operator concern; not part of this reference impl. |
| `NODE_VERSION` Dockerfile ARG pinned to 22 | 2026-05-06 | Tracks repo core belief #16. |
