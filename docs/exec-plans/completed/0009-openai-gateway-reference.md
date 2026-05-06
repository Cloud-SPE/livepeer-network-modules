# Plan 0009 — Reference OpenAI-compat gateway (option A)

**Status:** completed (2026-05-06)
**Opened:** 2026-05-06
**Closed:** 2026-05-06
**Owner:** initial author + assistant

## Goal

Build a reference OpenAI-compatible gateway in this monorepo that uses
the new wire spec end-to-end: real OpenAI Python/JS client SDKs hit
this gateway, which forwards through Livepeer middleware to the
capability-broker, which forwards to a mock backend that returns
OpenAI-shaped responses.

This is option A from the user's plan-0009 framing. Option B (migration
brief for the suite's existing `livepeer-openai-gateway`) is a separate
plan that follows once A ships.

## Why

- Validates the design under realistic OpenAI client traffic.
- Becomes the migration template the suite gateway can mirror.
- Provides the first end-to-end "real adopter" demo: gateway + broker +
  middleware + extractors all wired together.

## Scope (v0.1)

In scope:

- New component subfolder `openai-gateway/`. TypeScript Fastify service.
- Three OpenAI-compatible endpoints:
  - `POST /v1/chat/completions` (non-streaming + streaming via `stream: true`)
  - `POST /v1/embeddings`
  - `POST /v1/audio/transcriptions` (multipart upload)
- Each endpoint extracts the `model` field, builds a capability ID
  (`openai:<endpoint>:<model>`), picks the appropriate mode
  (`http-reqresp@v0` / `http-stream@v0` / `http-multipart@v0`), and
  forwards via inlined Livepeer middleware (mirrors the gateway-adapters
  API; switching to the real package via npm workspaces is tech-debt).
- Stub `Livepeer-Payment` blob (real payment-daemon integration is plan
  0005). Broker's mock-payment middleware accepts any non-empty value.
- Hardcoded broker URL via env var `LIVEPEER_BROKER_URL`.
- Compose stack: openai-gateway + broker + python mock-backend.
- Smoke test (`scripts/smoke.sh`) that exercises the stack with curl
  (mimicking OpenAI-shaped requests) and asserts response shape.

Out of scope (deferred):

- Customer auth (`Authorization: Bearer <api-key>`) — accept any value
  for v0.1; real auth is gateway-operator concern.
- Resolver integration (`service-registry-daemon`) — hardcoded broker
  URL for v0.1.
- Real `Livepeer-Payment` envelope minting — plan 0005.
- Postgres ledger, Stripe billing, free-tier — gateway-operator
  concerns.
- Streaming pass-through optimization. The middleware buffers the full
  response body to read trailers; the gateway therefore returns SSE
  data as a single delivered response. Format is correct; latency
  semantics differ. Tracked as tech-debt.
- True usage of `@tztcloud/livepeer-gateway-middleware` package.
  Inlined for v0.1; switch via file: dep + multi-stage Docker is
  follow-up.

## Outcomes

- [x] `openai-gateway/` scaffold landed (Dockerfile + Makefile +
  package.json + tsconfig.json + src + scripts + docs).
- [x] Inlined Livepeer client (`src/livepeer/`) for `http-reqresp@v0`
  + `http-stream@v0` + `http-multipart@v0`; mirrors gateway-adapters API.
- [x] Three Fastify route handlers (`src/routes/`) with model →
  capability ID translation.
- [x] `compose.yaml` + `test-broker-config.yaml` for end-to-end stack.
- [x] `scripts/smoke.sh` exercises the stack and asserts all four
  endpoints (10 assertions total).
- [x] `make smoke` runs the full stack and exits non-zero on failure.

## Done condition (met 2026-05-06)

```
$ make -C openai-gateway smoke
==> assertions
  PASS: POST /v1/chat/completions (non-streaming) returns 200
  PASS:   body has 'choices' field
  PASS:   body has 'usage' field
  PASS: POST /v1/chat/completions (streaming) returns 200
  PASS:   body has SSE 'data:' frames
  PASS:   body has [DONE] terminator
  PASS: POST /v1/embeddings returns 200
  PASS:   body has 'embedding' field
  PASS: POST /v1/audio/transcriptions returns 200
  PASS:   body has 'text' field
==> result: 10 passed, 0 failed
```

## Follow-on (separate plan number TBD)

- Plan 0013 (or similar) — Migration brief for the suite's existing
  `livepeer-openai-gateway`. Paper exercise; doesn't touch suite code.
  Picks up everything learned from this reference impl.
