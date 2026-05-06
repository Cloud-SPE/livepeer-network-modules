# Architecture

Reference OpenAI-compat gateway internals.

## Package layout

```
openai-gateway/
├── src/
│   ├── index.ts                  # entry point: loadConfig + buildServer + listen
│   ├── server.ts                 # Fastify wiring + multipart parser registration
│   ├── config.ts                 # env-derived config (brokerUrl, port, etc.)
│   ├── livepeer/                 # inlined Livepeer client
│   │   ├── headers.ts            # canonical Livepeer-* names + SPEC_VERSION
│   │   ├── errors.ts             # LivepeerBrokerError + errorFromResponse
│   │   ├── http-reqresp.ts       # send() via fetch
│   │   ├── http-stream.ts        # send() via node:http (trailer access)
│   │   └── http-multipart.ts     # send() via fetch + FormData
│   └── routes/
│       ├── chat-completions.ts   # POST /v1/chat/completions (stream/non-stream)
│       ├── embeddings.ts         # POST /v1/embeddings
│       └── audio-transcriptions.ts # POST /v1/audio/transcriptions
├── compose.yaml                  # gateway + broker + mock-backend
├── test-broker-config.yaml       # broker capabilities for the smoke stack
└── scripts/smoke.sh              # end-to-end test
```

## Request lifecycle (chat-completions, non-streaming)

1. OpenAI client POSTs to `http://gateway:3000/v1/chat/completions`
   with `{model, messages, stream: false}`.
2. Fastify routes to `chat-completions.ts` handler.
3. Handler reads `body.model`, builds capability ID
   `openai:chat-completions:<model>`. Picks `offering = "default"` and
   mode `http-reqresp@v0` (because stream=false).
4. Handler calls `httpReqresp.send({ brokerUrl, capability, offering, paymentBlob, body, contentType })`.
5. The send function builds the five required Livepeer-* request headers
   plus optional Livepeer-Request-Id, POSTs to `<brokerUrl>/v1/cap` via
   Node's `fetch`.
6. Broker validates payment + headers, dispatches to the
   configured backend (mock-backend), forwards body verbatim with
   Livepeer-* stripped and backend auth injected (none in v0.1).
7. Mock-backend returns OpenAI-shaped JSON.
8. Broker reads `Livepeer-Work-Units` via the `openai-usage` extractor,
   sets it on the response, returns to the gateway.
9. Handler reads the broker's response and returns it to the OpenAI
   client unchanged.

## Streaming chat-completions

Same shape, but step 4 uses `httpStream.send` (which uses Node's
`node:http` module so trailers are accessible). Step 9 returns the SSE
body to the OpenAI client.

**Limitation:** the http-stream client buffers the response body to
read trailers. The gateway therefore returns SSE atomically (all events
at once) rather than chunk-by-chunk. Format is correct; latency
semantics differ. Tracked as tech-debt for a future plan that improves
the streaming pass-through.

## What this gateway does NOT do

See [`../../DESIGN.md`](../../DESIGN.md) §"What this gateway does NOT do".
