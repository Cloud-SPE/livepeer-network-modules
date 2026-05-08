# openai-gateway

Reference OpenAI-compatible gateway demonstrating the new wire spec
end-to-end. Real OpenAI client SDKs hit this service → Livepeer
middleware → capability-broker → mock backend.

> **For agents:** start at [`AGENTS.md`](./AGENTS.md).

## What it is

A TypeScript Fastify service exposing the current OpenAI-compatible surface:

- `POST /v1/chat/completions` (with `stream: true` support)
- `POST /v1/embeddings`
- `POST /v1/audio/transcriptions`
- `GET /v1/realtime` (WebSocket upgrade)
- `POST /v1/images/generations`
- `POST /v1/audio/speech` stubbed behind a mode gate

Each endpoint:

1. Reads `model` from the request body / form-field.
2. Maps the request onto a workload capability ID.
3. Picks the right mode (`http-reqresp@v0` for non-streaming chat /
   embeddings; `http-stream@v0` for streaming chat;
   `http-multipart@v0` for transcriptions).
4. Selects a worker either from a static broker URL or via
   `service-registry-daemon`.
5. Mints a `Livepeer-Payment` envelope via the local payer-daemon.
6. Forwards to the selected broker via inlined Livepeer client.
7. Returns the broker's response to the OpenAI client.

## What it is not

- Production gateway. Customer auth / billing / ledger SaaS concerns
  still depend on the broader shell stack, and routing policy is still
  intentionally simple. This remains a reference impl for the wire +
  resolver shape, not a full product shell.

## Status

**v0.1** — the core endpoint set is implemented; smoke test runs the full
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

## Deployment

Two deployment shapes are now supported:

- Static broker routing via [compose/docker-compose.yml](./compose/docker-compose.yml)
- Manifest-driven routing via
  [compose/docker-compose.manifest-resolver.yml](./compose/docker-compose.manifest-resolver.yml)

The manifest-driven stack is the production-shaped topology for the
rewrite:

1. `service-registry-daemon` discovers active orchestrators from chain
2. it fetches and verifies each orch's published manifest URL
3. `openai-gateway` selects a route from the resolved pool
4. `payment-daemon` sender mode mints the `Livepeer-Payment` envelope
5. the gateway sends the request directly to the selected broker URL

Start from
[compose/.env.manifest-resolver.example](./compose/.env.manifest-resolver.example).

## Layout

```
openai-gateway/
├── package.json
├── tsconfig.json
├── Dockerfile / Makefile
├── compose.yaml                   # full smoke stack (gateway + broker + mock-backend)
├── compose/docker-compose.yml     # run-only deploy shape
├── test-broker-config.yaml        # broker config for smoke test
├── src/
│   ├── livepeer/                  # inlined middleware (mirrors @tztcloud/livepeer-gateway-middleware)
│   │   ├── headers.ts
│   │   ├── errors.ts
│   │   ├── http-reqresp.ts
│   │   ├── http-stream.ts
│   │   └── http-multipart.ts
│   ├── service/
│   │   ├── routeSelector.ts       # resolver/static route selection
│   │   └── routeDispatch.ts       # retry/fallback dispatch helpers
│   ├── routes/
│   │   ├── chat-completions.ts
│   │   ├── embeddings.ts
│   │   ├── audio-transcriptions.ts
│   │   ├── images-generations.ts
│   │   └── realtime.ts
│   ├── config.ts                  # env-based config
│   ├── server.ts                  # Fastify wiring
│   └── index.ts                   # entry point
├── scripts/smoke.sh
└── docs/
```
