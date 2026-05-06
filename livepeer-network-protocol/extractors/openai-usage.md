---
extractor_name: openai-usage
version: 0.1.0
status: draft (proposed)
spec_version: 0.1.0
last_updated: 2026-05-06
---

# Extractor: `openai-usage`

Reads token counts from the `usage` field of an OpenAI-shaped JSON response.

## When to use

- Any capability whose backend speaks the OpenAI API and returns a `usage`
  object: chat completions, embeddings, audio transcriptions (when the
  capability has been configured to return usage).

## Configuration in `host-config.yaml`

```yaml
work_unit:
  name: "tokens"
  extractor:
    type: "openai-usage"
    field: "total_tokens"     # default
```

| Field | Required | Default | Allowed values |
|---|---|---|---|
| `type` | yes | — | `"openai-usage"` |
| `field` | no | `"total_tokens"` | `"prompt_tokens"`, `"completion_tokens"`, `"total_tokens"` |

## Recipe

1. Parse the backend response body as JSON.
2. Look up `usage.<field>` (e.g., `usage.total_tokens`).
3. If present and integer-valued, that is `actualUnits`.
4. If absent or non-numeric, `actualUnits = 0` and broker SHOULD log a warning
   (the orch's offering is mis-configured or the backend is non-conformant).

## Streaming caveat

For `http-stream` mode against OpenAI chat-completions:

- The backend emits `usage` only in the **final** SSE event when
  `stream_options.include_usage: true` is set in the request.
- Broker MAY rewrite the request body to inject
  `stream_options.include_usage: true` when this extractor is active for an
  `http-stream`-mode capability (transparent to the gateway and customer).
- The final SSE event carries the usage object; broker reads it before
  emitting the `Livepeer-Work-Units` trailer.

## Example

Request body opaque; backend response:

```json
{
  "id": "chatcmpl-abc",
  "choices": [...],
  "usage": {
    "prompt_tokens": 24,
    "completion_tokens": 158,
    "total_tokens": 182
  }
}
```

With `extractor: { type: "openai-usage", field: "total_tokens" }`:
`actualUnits = 182`.

With `extractor: { type: "openai-usage", field: "completion_tokens" }`:
`actualUnits = 158`.

## Versioning

`0.1.0`. Per-extractor SemVer.

## Conformance

- Happy path: `usage.total_tokens` is correctly extracted.
- Missing usage field → `actualUnits = 0`; warning logged.
- Streaming: extractor reads from final SSE event when
  `stream_options.include_usage: true` is in the request body.
- Configurable field: extractor honors `field` parameter (`prompt_tokens` or
  `completion_tokens`).
