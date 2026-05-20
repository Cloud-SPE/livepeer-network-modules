# DESIGN

## What this component is

A reference TypeScript Fastify service that:

1. Accepts OpenAI-shaped requests on the customer-facing surface.
2. Translates each request to the new Livepeer wire spec
   (capability ID, offering, mode, headers).
3. Selects a broker either from static config or through
   `service-registry-daemon`.
4. Mints payments through the local payer-daemon with an explicit
   accepted-price basis, funding intent, and deterministic route/constraint
   fingerprints.
5. Forwards via Livepeer middleware to the selected capability-broker.
6. Returns the broker's response to the OpenAI client.

When the broker/payee reports `INVALID_RECIPIENT_RAND`, the gateway
reports that outcome back to the payer-daemon, retries payment minting
exactly once, and then replays the broker call with the fresh `work_id`.

Static broker mode is now an explicit compatibility path, not a
quote-free shortcut. It requires operator-supplied
`LIVEPEER_STATIC_PRICE_PER_WORK_UNIT_WEI` and `LIVEPEER_STATIC_WORK_UNIT`
so the gateway can still satisfy the payer-daemon's
`accepted_price` contract without a resolver.

This is the "first adopter" reference for the wire spec.

## Endpoint → mode mapping

| OpenAI endpoint | Capability template | Mode |
|---|---|---|
| `POST /v1/chat/completions` (stream: false) | `openai:chat-completions` | `http-reqresp@v0` |
| `POST /v1/chat/completions` (stream: true) | `openai:chat-completions` | `http-stream@v0` |
| `POST /v1/embeddings` | `openai:embeddings` | `http-reqresp@v0` |
| `POST /v1/audio/transcriptions` | `openai:audio-transcriptions` | `http-multipart@v0` |

The model is read from the JSON request body (chat/embeddings) or from
the `model` form-field (audio). The capability template is interpolated
to produce the `Livepeer-Capability` header value.

## What this gateway does NOT do (deferred)

- **Customer auth.** Accepts any `Authorization: Bearer <token>` value.
  Real per-API-key auth is operator-side.
- **Postgres ledger / Stripe / free-tier.** Operator concerns.
- **Advanced resolver policy.** The gateway now routes from
  `service-registry-daemon`, but only v1 policy ships: hard
  `constraints`, soft `extra` preference, lowest-price tie-break, and
  simple retry on the next candidate.

## Internal architecture

See
[`docs/design-docs/architecture.md`](./docs/design-docs/architecture.md)
for the planned package layout.

## Stack composition for `make smoke`

```
┌──────────────┐        ┌──────────┐        ┌──────────────┐
│  curl (host) │ ──→    │ openai-  │ ──→    │  capability- │ ──→ ┌────────────────┐
│              │        │ gateway  │        │  broker      │     │  mock-backend  │
│ OpenAI-shape │        │ (this)   │        │  (Go)        │     │  (Python http) │
└──────────────┘        └──────────┘        └──────────────┘     └────────────────┘

       OpenAI wire           Livepeer-* headers + /v1/cap         opaque backend body
```

The mock-backend returns OpenAI-shaped responses; the broker forwards
verbatim; the gateway returns to curl.
