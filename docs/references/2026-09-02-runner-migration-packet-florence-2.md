# Runner migration packet — `florence-2-runner`

Date: 2026-09-02. For the author of `shane-demo/florence-2-runner`
(`moatus/florence-2-runner`). Based on a read of the repository on
2026-09-01/02; quoted paths are from your source. Tracked as `lnm-6ig`.

**Dependency order (decision 4):** nothing here waits on anything else.
The catalog template (`vision-image-analysis-florence-2-large.yaml`) is
already written to this shape; the runner is independently verifiable the
moment it ships.

## The short version

The pool sells Florence as **one capability, `vision:image-analysis`, on
one route, `POST /v1/vision/analyze`**. The runner declares that in a
contract document the pool reads once; the `/openai:vision/options` describe
surface is gone. `openai:vision` was never a real capability — OpenAI has no
vision endpoint — and Florence is not a chat model, so the chat-shaped route
is not what is sold either (decision 2, option C).

## What you can delete

| What you have | Why it existed | What replaces it |
|---|---|---|
| `GET /openai:vision/options` (`app.py`) | The v0 broker dialled runners for a describe payload | `GET /.well-known/livepeer-runner`. Nothing reads `/options`. |
| `DEFAULT_CAPABILITY_NAME = "florence-2"` with `init=False` | Fixed name | Default `vision:image-analysis`, overridable by `CAPABILITY_NAME`. |
| The v0 modes vocabulary in the options payload (`openai-chat-completions-vision@v1`, `streaming_modes`) | Mode negotiation | `transports: ["unary"]`. |

**Recommended, your call (decision 12):** `POST /v1/chat/completions`,
`POST /infer/lmm`, `POST /infer/lmm/{model_id}`. The pool certifies and
dispatches one path and never reaches these. They are three surfaces over
one model to keep consistent and tested, and the chat one was the source
of a real confusion about what this runner is. Deleting them is not
required by anything in `livepeer-network-modules`.

## What changes

### The contract

```json
{
  "capability_id": "vision:image-analysis",
  "protocol": "paid-job/v1",
  "transports": ["unary"],
  "work_unit": { "name": "images",
                 "extractor": { "type": "response-header", "name": "X-Livepeer-Work-Units" } },
  "paths": { "invoke": "/v1/vision/analyze" },
  "readiness": { "type": "http-status", "path": "/healthz" },
  "identity": { "model": "florence-2-large", "provider": "florence" },
  "schema_versions": { "paid-job/v1": "1.0.15" }
}
```

Three things to notice:

- **`identity.model`, not `identity.openai.model`.** This is not an OpenAI
  capability, so it does not use the OpenAI identity key. The catalog's
  `match` is `identity.model: florence-2-large`; a runner declaring the
  `openai.model` key matches no template.
- **The extractor is yours to declare.** You already emit
  `X-Livepeer-Work-Units` with the image count; the contract says so, and
  the broker reads it. The catalog's smoke step also asserts
  `$.usage.images`, so the header and the body must agree.
- **Readiness is `/healthz`**, which you serve. `http-openai-model-ready`
  against `/v1/models` was the earlier plan; it is the OpenAI probe and this
  is not an OpenAI runner.

### The request the pool certifies with

```json
{ "model": "florence-2-large", "image": "data:image/png;base64,…", "task": "caption" }
```

asserting `$.results[0].response` and `$.usage.images`. That is your
existing `VisionAnalyzeRequest`; no change to the route's shape is asked
for.

## What is *not* fixed by this work

- The image tag. The catalog omits `runner_compose.image` until you tell us
  the tag that ships the contract (`lnm-v12`).
- The workflow kit's Florence manifest still hard-codes `florence-2`; that
  is the kit's packet.

## Verifying

```sh
cd livepeer-network-protocol/conformance
go run ./cmd/livepeer-conformance --broker-url http://<broker>:8080 --serve-runner moatus/florence-2-runner:<tag>
```

Then against a pool: attach a host with a 4090; the template places;
certification's smoke evidence shows a caption; the usage step reports 1.

## What we need from you

1. The tag that serves the contract.
2. Whether you kept or deleted the other three routes (for our records
   only).
