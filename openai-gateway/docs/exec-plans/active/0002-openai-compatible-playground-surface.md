# 0002 OpenAI-Compatible Playground Surface

## Why

The current customer playground is a smoke tool, not a real OpenAI-compatible UI.
It hardcodes a narrow subset of request parameters per capability and renders most
responses as raw JSON blobs. That is not sufficient for operators or customers who
need to exercise the gateway as an OpenAI-compatible surface.

We need one source of truth that drives both:

1. the gateway-facing understanding of each OpenAI-compatible capability
2. the customer playground request/response UI

The design must tolerate OpenAI adding optional request parameters and response
properties over time.

## Goals

1. Represent the OpenAI-compatible request/response surface for every paid
   capability in checked-in code.
2. Expose that surface through gateway catalog APIs so the playground can adapt by
   capability and by model.
3. Replace the current hardcoded playground with schema-driven forms and
   schema-aware response renderers.
4. Preserve a raw mode so newly added OpenAI fields can still be exercised before
   the polished UI catches up.

## Non-goals

1. Perfect parity with every provider-specific extension on day one.
2. Runtime scraping of OpenAI docs from the browser.
3. Hiding raw responses; raw access remains required.

## Sources

- OpenAI API overview:
  `https://developers.openai.com/api/reference/overview`
- Chat Completions:
  `https://platform.openai.com/docs/api-reference/chat/create-chat-completion`
- Embeddings:
  `https://developers.openai.com/api/reference/resources/embeddings/methods/create`
- Images:
  `https://platform.openai.com/docs/api-reference/images/generate`
- Image object:
  `https://platform.openai.com/docs/api-reference/images/object?lang=node.js`
- Audio transcriptions:
  `https://platform.openai.com/docs/api-reference/audio/transcriptions`
- Audio speech:
  `https://platform.openai.com/docs/api-reference/audio/speech`

## Design

### 1. Checked-in surface schema

Add a checked-in schema module under `src/service/openaiSurface.ts` that defines, per
capability:

- request transport kind: json, multipart, binary
- request fields:
  - name
  - type
  - label
  - help text
  - required/optional
  - advanced/basic
  - enum options where applicable
- response variants:
  - json
  - stream
  - binary
  - structured variants such as text, verbose JSON, image gallery, embeddings list
- notes about model-dependent support

This schema is the source of truth for both gateway catalog metadata and playground
rendering.

### 2. Gateway catalog enrichment

Extend `/v1/models` and `/portal/playground/catalog` to include capability surface
metadata. Model catalog entries should expose:

- `supported_modes`
- `surface.request_transport`
- `surface.request_fields`
- `surface.response_variants`
- `surface.raw_supported`
- `surface.model_dependent_fields`

The API remains OpenAI-compatible for request execution, while the portal catalog
becomes the source of truth for UI adaptation.

### 3. Playground rebuild

Replace the current one-form switch statement with:

- capability-specific request builders
- schema-driven field rendering
- raw request editor mode
- response renderer tabs:
  - guided
  - raw

Initial guided renderers:

- chat:
  - messages editor
  - generation controls
  - stream toggle
  - tool/structured output fields
- embeddings:
  - single input and batch input
  - dimensions and encoding format
- images:
  - prompt
  - size/quality/output format
  - image preview gallery
- audio transcriptions:
  - multipart file upload
  - response format and timestamps
  - transcript and verbose response renderers
- audio speech:
  - input/voice/format/speed
  - audio player renderer

### 4. Validation and compatibility

The playground must validate obvious cross-field combinations and disable options
that the selected model or capability manifest does not support.

Examples:

- `stream_options` only valid when `stream=true`
- transcription timestamp granularities require compatible response format
- image output settings depend on selected output format

### 5. Testing

- unit tests for the surface schema and request builders
- API tests for catalog exposure
- frontend tests for schema-driven rendering and response variant selection

## Execution phases

### Phase 2A: Surface foundation

1. Add checked-in capability surface schema.
2. Expose surface metadata from catalog endpoints.
3. Show the schema in the playground sidebar so users can inspect supported request
   fields and response variants immediately.

### Phase 2B: Guided request forms

1. Replace current hardcoded payload builders with schema-driven request builders.
2. Add per-capability form sections and advanced settings.
3. Keep raw mode available for every capability.

### Phase 2C: Guided response renderers

1. Add capability-specific renderers for chat, embeddings, images, speech, and
   transcription responses.
2. Keep raw tabs for all responses.

### Phase 2D: Hardening

1. Add compatibility matrix support.
2. Add stronger model-aware validation.
3. Add regression tests around the schema layer.

## Status

- Completed:
  - Phase 2A foundation:
    - checked-in capability surface schema
    - catalog/model API exposure of surface metadata
    - surface visibility in the customer playground
  - Core Phase 2B and 2C implementation:
    - schema-driven guided request editor
    - raw request mode
    - guided/raw response tabs
    - typed response renderers for chat, embeddings, images, speech, and transcriptions
- Remaining hardening:
  - finer model-specific compatibility gating beyond interaction modes
  - broader frontend UI regression coverage
