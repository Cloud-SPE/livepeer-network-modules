# openai-embeddings-runner

OpenAI-embeddings proxy that sits between the capability broker and a
vLLM-in-embed-mode or Ollama backend. Reads `usage.total_tokens` from
the response body and emits it as an `X-Livepeer-Work-Units` HTTP
response header the broker bills against.

Mirrors the operational shape of `openai-chat-runner` — same
`/options` discovery pattern, same `UPSTREAM_KIND` env hook, same
header-stripping security boundary — but simpler internally since
embeddings is request/response (no SSE, no trailer).

## Endpoints

| Method | Path | Purpose |
|---|---|---|
| POST | `/v1/embeddings` | Forward to upstream; emit X-Livepeer-Work-Units response header |
| GET  | `/healthz` | 200 once upstream model discovery succeeds |
| GET  | `/<capability>/options` | Structured options payload for broker hydration |

## Configuration

### Core runtime

| Env var | Default | Purpose |
|---|---|---|
| `RUNNER_ADDR` | `:8080` | HTTP bind |
| `UPSTREAM_URL` | (required) | Upstream OpenAI-compatible embeddings endpoint. Examples: `http://vllm_embeddings:8000/v1/embeddings`, `http://ollama:11434/v1/embeddings` |
| `UPSTREAM_KIND` | `vllm` | One of `vllm`, `ollama`. Advertised in `/options`; same code path today. |
| `CAPABILITY_NAME` | `openai-text-embeddings` | Path segment for the `/options` endpoint |
| `USAGE_FIELD` | `total_tokens` | Which `usage.*` field to bill on (`prompt_tokens` or `total_tokens`) |
| `MODEL_DISCOVERY_RETRIES` | `10` | Startup retries against upstream `/v1/models` |

### Discovery metadata (surfaced via `/<capability>/options`)

The broker reads the options endpoint and merges these fields into the
broker's host-config `extra:` block (operator entries always win).

| Env var | Maps to broker `extra.*` |
|---|---|
| `SERVED_MODEL_NAME` | `served_model_name` (defaults to first model from upstream `/v1/models`) |
| `BACKEND_MODEL` | `backend_model` (HuggingFace path or other identifier) |
| `EMBEDDING_DIMENSIONS` | `embedding_dimensions` (integer) |
| `MAX_INPUT_TOKENS` | `max_input_tokens` (integer) |
| `POOLING_MODE` | `pooling_mode` (e.g. `cls`, `mean`) |

## Broker wiring

```yaml
- id: "openai:embeddings"
  offering_id: "bge-large-en-v1.5"
  interaction_mode: "http-reqresp@v0"
  work_unit:
    name: "tokens"
    extractor:
      type: "response-header"
      header: "X-Livepeer-Work-Units"
      default: 0
  health:
    probe:
      type: "http-status"
      config: { url: "http://openai_embeddings_runner:8080/healthz" }
  backend:
    transport: "http"
    url: "http://openai_embeddings_runner:8080/v1/embeddings"
    auth: "none"
  extra:
    openai:
      model: "bge-large-en-v1.5"
    provider: "openai-embeddings-runner"
```

## Build

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -t tztcloud/openai-embeddings-runner:v1.2.0 .
```
