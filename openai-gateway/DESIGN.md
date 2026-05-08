# DESIGN

## What this component is

A reference TypeScript Fastify service that:

1. Accepts OpenAI-shaped requests on the customer-facing surface.
2. Translates each request to the new Livepeer wire spec
   (capability ID, offering, mode, headers).
3. Selects a broker either from static config or through
   `service-registry-daemon`.
4. Mints payments through the local payer-daemon.
5. Forwards via Livepeer middleware to the selected capability-broker.
6. Returns the broker's response to the OpenAI client.

This is the "first adopter" reference for the wire spec.

## Endpoint → mode mapping

| OpenAI endpoint | Capability template | Mode |
|---|---|---|
| `POST /v1/chat/completions` (stream: false) | `openai:chat-completions:<model>` | `http-reqresp@v0` |
| `POST /v1/chat/completions` (stream: true) | `openai:chat-completions:<model>` | `http-stream@v0` |
| `POST /v1/embeddings` | `openai:embeddings:<model>` | `http-reqresp@v0` |
| `POST /v1/audio/transcriptions` | `openai:audio-transcriptions:<model>` | `http-multipart@v0` |

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
