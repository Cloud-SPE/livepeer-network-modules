# openai-gateway

Reference OpenAI-compatible gateway demonstrating the new wire spec
end-to-end. Real OpenAI client SDKs hit this service → Livepeer
middleware → capability-broker → mock backend.

> **For agents:** start at [`AGENTS.md`](./AGENTS.md).

## What it is

A TypeScript Fastify service exposing three OpenAI-compatible endpoints:

- `POST /v1/chat/completions` (with `stream: true` support)
- `POST /v1/embeddings`
- `POST /v1/audio/transcriptions`

Each endpoint:

1. Reads `model` from the request body / form-field.
2. Builds a capability ID: `openai:<endpoint>:<model>`.
3. Picks the right mode (`http-reqresp@v0` for non-streaming chat /
   embeddings; `http-stream@v0` for streaming chat;
   `http-multipart@v0` for transcriptions).
4. Forwards to the broker via inlined Livepeer client.
5. Returns the broker's response to the OpenAI client.

## What it is not

- Production gateway. No real customer auth, no payment-daemon
  integration, no billing ledger, no resolver. v0.1 is a reference
  impl that proves the wire shape end-to-end.

## Status

**v0.1** — three endpoints implemented; smoke test runs the full
stack via Docker compose. Tracked in
[plan 0009](../docs/exec-plans/completed/0009-openai-gateway-reference.md).

## Build + smoke

Per core belief #15, every gesture is Docker-first.

```bash
make build               # build the openai-gateway image
make smoke               # spin up compose stack + run smoke test
make help                # show all targets
```

No host `node` install required.

## Layout

```
openai-gateway/
├── package.json
├── tsconfig.json
├── Dockerfile / Makefile
├── compose.yaml                   # full stack (gateway + broker + mock-backend)
├── test-broker-config.yaml        # broker config for smoke test
├── src/
│   ├── livepeer/                  # inlined middleware (mirrors @tztcloud/livepeer-gateway-middleware)
│   │   ├── headers.ts
│   │   ├── errors.ts
│   │   ├── http-reqresp.ts
│   │   ├── http-stream.ts
│   │   └── http-multipart.ts
│   ├── routes/
│   │   ├── chat-completions.ts
│   │   ├── embeddings.ts
│   │   └── audio-transcriptions.ts
│   ├── config.ts                  # env-based config
│   ├── server.ts                  # Fastify wiring
│   └── index.ts                   # entry point
├── scripts/smoke.sh
└── docs/
```
