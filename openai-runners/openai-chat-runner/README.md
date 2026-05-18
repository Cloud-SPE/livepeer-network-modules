# openai-chat-runner

Streaming-aware OpenAI chat-completions proxy that sits between the
capability broker and a vLLM or Ollama backend (or any
OpenAI-compatible upstream). Its added value over a transparent proxy
is **token counting for streaming requests**:

- For `stream: true` requests, the runner scans the upstream SSE
  response, accumulates the final `usage.total_tokens` value, and
  reports it back to the broker via the `X-Livepeer-Work-Units` HTTP
  trailer.
- For non-streaming requests, the runner passes the response through
  unchanged; the broker reads `usage.total_tokens` from the body with
  the existing `openai-usage` extractor.

This matches the trailer-based work-unit reporting pattern the
capability-broker `http-stream@v0` driver already uses for its
gateway-facing side.

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| POST | `/v1/chat/completions` | Forward to upstream; emit work-units trailer on streaming responses |
| GET  | `/healthz` | 200 once upstream model discovery succeeds |
| GET  | `/<capability>/options` | Structured options payload for broker hydration |

## Configuration

### Core runtime

| Env var | Default | Purpose |
|---|---|---|
| `RUNNER_ADDR` | `:8080` | HTTP bind |
| `UPSTREAM_URL` | (required) | Upstream OpenAI-compatible chat-completions endpoint, e.g. `http://vllm_model_runner:8000/v1/chat/completions` or `http://ollama:11434/v1/chat/completions` |
| `UPSTREAM_KIND` | `vllm` | One of `vllm`, `ollama`. Advertised in the `/options` payload; same code path today (future hook for kind-specific quirks). |
| `CAPABILITY_NAME` | `openai-chat-completions` | Path segment for the `/options` endpoint |
| `USAGE_FIELD` | `total_tokens` | Which `usage.*` field to bill on (`prompt_tokens`, `completion_tokens`, or `total_tokens`) |
| `MODEL_DISCOVERY_RETRIES` | `10` | Startup retries against upstream `/v1/models` |

### Discovery metadata (surfaced via `/<capability>/options`)

The capability broker reads the runner's options endpoint and merges
the returned fields into the broker's host-config `extra:` block
(operator-set values in host-config always win). All of these are
optional — unset fields are simply omitted from the options payload.

| Env var | Maps to broker `extra.*` | Notes |
|---|---|---|
| `SERVED_MODEL_NAME` | `served_model_name` | Defaults to the first model returned by upstream `/v1/models` |
| `BACKEND_MODEL` | `backend_model` | HuggingFace path or other upstream identifier |
| `CONTEXT_LENGTH` | `context_length` | Integer; advertise the max model length you serve |
| `REASONING_PARSER` | `reasoning_parser` | e.g. `qwen3` |
| `TOOL_CALL_PARSER` | `tool_call_parser` | e.g. `qwen3_coder` |
| `QUANTIZATION` | `quantization` | e.g. `modelopt` |

Derived `features` flags (always present in the options payload):

- `streaming: true` — this runner exists to count streaming tokens.
- `include_usage_required: true` — vLLM only emits the final usage
  frame when the request asks for it; the runner injects the flag.
- `reasoning: true` — present iff `REASONING_PARSER` is set.
- `tool_calling: true` — present iff `TOOL_CALL_PARSER` is set.

## Client request shape

For streaming requests, the runner **auto-injects**
`stream_options.include_usage: true` if absent.

- **vLLM** honours `include_usage` on all supported releases. Works
  out of the box.
- **Ollama** honours `include_usage` starting in v0.5. Older
  Ollama silently drops the flag — the final SSE frame has no
  `usage` object and the runner bills 0 tokens. Operators on older
  Ollama should upgrade before deploying this runner.

Clients can pre-set the flag explicitly (including
`include_usage: false`, which the runner honours).

## Broker wiring

In the broker host-config, point the stream offering's `backend.url`
at the runner and use the `response-trailer` extractor:

```yaml
- id: "openai:chat-completions"
  offering_id: "vllm-qwen3.6-27b-stream"
  interaction_mode: "http-stream@v0"
  work_unit:
    name: "tokens"
    extractor:
      type: "response-trailer"
      trailer: "X-Livepeer-Work-Units"
      default: 0
  backend:
    transport: "http"
    url: "http://openai_chat_runner:8080/v1/chat/completions"
    auth: "none"
  extra:
    openai:
      model: "Qwen3.6-27B"
    provider: "openai-chat-runner"
```

## Build

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -t tztcloud/openai-chat-runner:v1.3.0 .
```
