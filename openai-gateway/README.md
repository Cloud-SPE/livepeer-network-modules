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
- `GET /portal/*` customer portal UI
- `GET /admin/console/*` operator admin UI

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

The same runtime now also mounts the first gateway shell routes:

- `POST /portal/signup`
- `POST /portal/login` (API-key-based)
- `GET /portal/account`
- `GET /portal/api-keys`
- `POST /portal/api-keys`
- `GET /portal/topups`
- `GET /portal/usage`
- `GET /portal/usage/:id`
- `POST /portal/topups/checkout` when Stripe is configured
- `POST /portal/stripe/webhook` when Stripe is configured
- `POST /admin/customers`
- `GET /admin/customers`
- `GET /admin/customers/:id`
- `POST /admin/customers/:id/balance`
- `POST /admin/customers/:id/status`
- `POST /admin/customers/:id/refund`
- `GET /admin/topups`
- `GET /admin/reservations`
- `GET /admin/reservations/:id`
- `GET /admin/audit`
- `GET /admin/openai/rate-card`
- `PUT /admin/openai/rate-card`
- `GET /admin/openai/resolver-candidates`

## What it is not

- Final production product shell. The portal/admin surface is now real
  and deployable, but still intentionally thin relative to the older
  bridge: API-key-based customer auth, no full password auth flow, and a v1
  playground.
  Usage history is reservation-ledger based today, with settled cost/token
  fields persisted per reservation rather than a separate analytics product.
  The admin console now exposes the same ledger across customers for operator
  inspection, plus per-request drilldown, but it is still a ledger view rather
  than a full analytics suite.
  Rate-card management is currently JSON-snapshot based rather than a
  polished field-by-field editor.

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

When the shell is enabled, the same host serves:

- customer portal: `/portal/`
- operator console: `/admin/console/`

Optional Stripe-backed billing requires:

- `OPENAI_GATEWAY_PUBLIC_BASE_URL`
- `STRIPE_SECRET_KEY`
- `STRIPE_WEBHOOK_SECRET`

Admin console auth requires:

- `OPENAI_GATEWAY_ADMIN_TOKENS` as a comma-separated token list
- every admin request to send `Authorization: Bearer <token>` and `X-Actor: <operator-name>`

and exposes:

- customer checkout init: `POST /portal/topups/checkout`
- Stripe webhook: `POST /portal/stripe/webhook`

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
│   │   ├── customer-portal.ts
│   │   ├── embeddings.ts
│   │   ├── audio-transcriptions.ts
│   │   ├── images-generations.ts
│   │   └── realtime.ts
│   ├── frontend/
│   │   ├── portal/               # bundled customer SPA
│   │   └── admin/                # bundled operator SPA
│   ├── config.ts                  # env-based config
│   ├── server.ts                  # Fastify wiring
│   └── index.ts                   # entry point
├── scripts/smoke.sh
└── docs/
```
