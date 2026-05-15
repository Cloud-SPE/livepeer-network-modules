# rerank-runner — operator runbook

Cohere-compatible reranker (`/v1/rerank`). Loads
`zeroentropy/zerank-2`, a 4-billion-parameter Qwen3-based CrossEncoder.

## Image build + tag pin

amd64-only per OQ4.

```bash
docker build -t tztcloud/rerank-runner:v1.1.0 .
```

Default tag: **v1.1.0**.

## GPU prerequisites

NVIDIA GPU with Pascal+ architecture (matches the rewrite-wide GPU
lock at `../docs/design-docs/gpu-requirements.md`):

- Driver: NVIDIA 535+.
- nvidia-container-toolkit installed.
- Compose: `runtime: nvidia`.

zerank-2 (4-billion params, bfloat16) needs ~8 GB VRAM. Fits on
RTX 3090 / 4080 / A100 / L40 / RTX 4090.

## DEVICE=cpu fallback

CPU reranking is feasible but slow (~30s for 100 docs). Operator-side
fallback: `DEVICE=cpu`. The fail-fast probe applies if `DEVICE=cuda` is
set without a GPU.

## Model setup

Pre-pull via the sibling `model-downloader` image:

```bash
docker run --rm \
  -v ai-rerank-models:/models \
  -e MODEL_ID="zeroentropy/zerank-2" \
  tztcloud/rerank-model-downloader:v1.1.0
```

zerank-2 is ~8 GB on disk.

## Multi-arch matrix per OQ4

| Image | Platforms |
|---|---|
| `rerank-runner:v1.1.0` | linux/amd64 only |

## Prometheus integration (OQ5)

Set `METRICS_ENABLED=true` to expose `/metrics`. Default-off.

## Tuning

- `MAX_BATCH_SIZE` — per-request doc cap (default 1000); raise for
  large-corpus workloads at the cost of memory pressure.
- `INFERENCE_BATCH_SIZE` — internal `model.predict()` batch (default
  64); raise on bigger GPUs for higher throughput.
- `MAX_QUEUE_SIZE` — concurrent request cap (default 5); 429 when
  exceeded.

## Healthcheck

`GET /healthz` returns 200 once the CrossEncoder finishes loading +
warmup pass.

## Capability registration

The orch-coordinator scrapes `GET /rerank/options` per plan 0018.
