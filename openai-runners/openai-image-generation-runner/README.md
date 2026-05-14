# openai-image-generation-runner

Python FastAPI workload binary serving `/v1/images/generations`
(diffusers; SDXL / RealVisXL / FLUX).

## Endpoints

| Method | Path | Capability |
|---|---|---|
| POST | `/v1/images/generations` | `image-generation` |
| GET | `/healthz` | — |
| GET | `/options` and `/image-generation/options` | — |
| GET | `/metrics` | opt-in via `METRICS_ENABLED=true` (per OQ5) |

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `CAPABILITY_NAME` | `image-generation` | Capability identity (per OQ1) |
| `MODEL_ID` | (required) | HuggingFace diffusers model id |
| `MODEL_DIR` | `/models` | Local model cache |
| `RUNNER_PORT` | `8080` | HTTP bind |
| `DEVICE` | `cuda` | torch device; fail-fast on `cuda` + no GPU (per OQ3) |
| `DTYPE` | `float16` | torch dtype |
| `MAX_QUEUE_SIZE` | `5` | 429 threshold |
| `USE_TORCH_COMPILE` | `false` | Toggle torch.compile() |
| `DEFAULT_WIDTH` | `1024` | Default image width |
| `DEFAULT_HEIGHT` | `1024` | Default image height |
| `DEFAULT_STEPS` | model-dependent | Inference steps |
| `DEFAULT_GUIDANCE` | model-dependent | Guidance scale |
| `METRICS_ENABLED` | `false` | Opt-in `/metrics` Prometheus endpoint (per OQ5) |
| `TRITON_CACHE_DIR` | `/cache/triton` | Persistent kernel cache |

The runner detects the model family from the `MODEL_ID` and applies
sensible defaults (FLUX vs RealVisXL vs SDXL).

## Build

amd64-only per OQ4. CUDA 12.9 base image.

```bash
docker build -t tztcloud/openai-image-generation-runner:v1.0.0 .
```

## Source attribution

Ported from `livepeer-byoc/openai-runners/openai-image-generation-runner/app.py`
(refactored: split FastAPI app + diffusers_loader + gpu_probe;
`requirements.txt` → `pyproject.toml`; added GPU fail-fast probe per OQ3
+ opt-in /metrics endpoint per OQ5).
