# python-runner-base

Shared Python base image for the workload binaries that share a common
HTTP server stack. Per OQ2 lock in plan 0013-runners-byoc-migration.

## Image

`tztcloud/python-runner-base:v1.0.0`.

Inherits `python:3.12-slim` and pre-installs the canonical Python pins:

| Package | Pin |
|---|---|
| `fastapi` | `>=0.115.0,<0.117.0` |
| `pydantic` | `>=2.9.0,<3.0.0` |
| `pydantic-settings` | `>=2.5.0,<3.0.0` |
| `structlog` | `>=24.4.0,<25.0.0` |
| `uvicorn[standard]` | `>=0.30.0,<0.33.0` |
| `prometheus-client` | `>=0.21.0,<0.22.0` |
| `python-multipart` | `>=0.0.12,<0.1.0` |

System packages: `ca-certificates`, `curl`, `ffmpeg`. This base is for
CPU Python tooling and non-CUDA consumers. The GPU-backed OpenAI
runners now inherit from sibling `python-gpu-runner-base/`; they no
longer use this image directly.

## How per-runner Dockerfiles inherit it

```dockerfile
ARG BASE_IMAGE=tztcloud/python-runner-base:v1.0.0
FROM ${BASE_IMAGE}

# Add only model-specific deps:
RUN pip install --no-cache-dir \
    torch torchaudio \
    --index-url https://download.pytorch.org/whl/cu128

COPY pyproject.toml ./
RUN pip install --no-cache-dir -e .

COPY src/ ./src/
ENV CAPABILITY_NAME=...
CMD ["python", "-m", "<runner_name>"]
```

Each CPU-oriented runner's `pyproject.toml` declares **only**
model-specific deps — the common deps live here. GPU-backed runners use
`python-gpu-runner-base/` instead.

## Build

```bash
docker build -t tztcloud/python-runner-base:v1.0.0 .
```

Or from the parent component:

```bash
make -C .. base       # via openai-runners/Makefile
```

## What this base does NOT include

- **No torch.** Each runner installs its own torch wheel against the
  CUDA version it needs (CUDA 12.8 in current pins; matches RTX 4090 +
  5090 hardware).
- **No model weights.** Per-runner sibling `image-model-downloader/` or
  `model-downloader/` images pre-pull weights into a shared volume.
- **No CUDA runtime.** ML runners use `nvidia/cuda:*-runtime-ubuntu22.04`
  as their actual base, not this one. This image is for runners that
  fit on `python:3.12-slim` (rerank-runner has tested both layouts).

(See plan 0013-runners-byoc-migration §6.7 for the full OQ2 lock.)
