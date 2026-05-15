# openai-runner — operator runbook

Pure-Go proxy in front of an upstream OpenAI-compatible server (Ollama
or vLLM). Two binaries in one image: `chat` and `embeddings`.

## Image build + tag pin

Multi-arch (amd64 + arm64) per OQ4. The Go runner has no GPU
dependency.

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  --target chat \
  -t tztcloud/openai-runner-chat:v1.1.0 \
  ../openai-runner

docker buildx build --platform linux/amd64,linux/arm64 \
  --target embeddings \
  -t tztcloud/openai-runner-embeddings:v1.1.0 \
  ../openai-runner
```

Default tag is **v1.1.0**. Keep related runner/base images on the same
tag unless the caller overrides `TAG=...`.

## GPU prerequisites

**None.** This runner is a pure-Go HTTP proxy; the upstream Ollama /
vLLM server is responsible for GPU passthrough. See those projects'
docs for nvidia-container-toolkit + driver setup.

## DEVICE fallback

This runner has no `DEVICE` knob — it doesn't load a model. The
upstream server handles device selection.

## Multi-arch matrix per OQ4

| Image | Platforms | Notes |
|---|---|---|
| `openai-runner-chat:v1.1.0` | linux/amd64, linux/arm64 | Use the arm64 build on Apple Silicon dev machines or AWS Graviton |
| `openai-runner-embeddings:v1.1.0` | linux/amd64, linux/arm64 | same |

Multi-arch is enabled because the runner is pure-Go with no native
deps and the proxy footprint is small (~15 MB per arch).

## Prometheus integration (OQ5)

Set `METRICS_ENABLED=true` to expose `/metrics`. Default-off; zero
overhead when unset. Cardinality-capped to `model` + `offering` labels.

## Common tuning

- `MAX_BODY_BYTES` — defaults to 5 MB for chat, 1 MB for embeddings.
  Raise via env if your upstream supports larger payloads.
- `MODEL_DISCOVERY_RETRIES` — default 10, with 10s between tries.
  Raise for slow-starting upstream servers.

## Healthcheck

`GET /healthz` returns 503 until upstream model discovery succeeds; 200
once the runner sees `/v1/models`. Broker should treat 503 as "not
ready" and skip dispatch.

## Capability registration

The orch-coordinator scrapes `GET /<capability>/options` (per plan
0018); no registration sidecar needed.
