# openai-runner

Go binary that proxies OpenAI-compatible chat completions and embeddings
requests to an upstream Ollama or vLLM server. Two targets in one Docker
image: `chat` and `embeddings`.

## Endpoints

| Method | Path | Capability | Build target |
|---|---|---|---|
| POST | `/v1/chat/completions` | `openai-chat-completions` | `chat` |
| POST | `/v1/embeddings` | `openai-text-embeddings` | `embeddings` |
| GET | `/healthz` | both |
| GET | `/<capability>/options` | both |

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `CAPABILITY_NAME` | per build target | Capability identity (per OQ1) |
| `RUNNER_ADDR` | `:8080` | HTTP bind |
| `UPSTREAM_URL` | (required) | Upstream OpenAI-compatible endpoint |
| `MODEL_DISCOVERY_RETRIES` | `10` | At-startup retries against upstream `/v1/models` |
| `METRICS_ENABLED` | `false` | Opt-in `/metrics` Prometheus endpoint (per OQ5) |

The proxy strips `Livepeer` and `Authorization` headers before forwarding
upstream. Returns 503 from `/healthz` until upstream model discovery
succeeds.

## Build

Multi-arch (amd64 + arm64) per OQ4. Pure Go, no GPU needed for the
proxy itself.

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  --target chat \
  -t tztcloud/openai-runner-chat:v0.8.10 .

docker buildx build --platform linux/amd64,linux/arm64 \
  --target embeddings \
  -t tztcloud/openai-runner-embeddings:v0.8.10 .
```

## Source attribution

Ported from `livepeer-byoc/openai-runners/openai-runner/` (Go module
renamed from `openai_runner` to
`github.com/Cloud-SPE/livepeer-network-rewrite/openai-runners/openai-runner`).
