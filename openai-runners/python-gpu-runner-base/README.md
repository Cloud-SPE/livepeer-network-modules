# python-gpu-runner-base

Shared GPU Python base image for the CUDA-backed workload binaries in
`openai-runners/`. It bakes in the common Python HTTP server stack on
top of the canonical NVIDIA runtime image, so GPU runners add only
runner-specific OS packages, torch extras, and app deps.

## Image

`tztcloud/python-gpu-runner-base:v1.1.0`.

Base image: `nvidia/cuda:12.9.1-runtime-ubuntu24.04`.

Preinstalled:

- `ca-certificates`
- `python3`
- `python3-pip`
- `python3-venv`
- `/opt/venv` with:
  - `fastapi`
  - `pydantic`
  - `pydantic-settings`
  - `structlog`
  - `uvicorn[standard]`
  - `prometheus-client`
  - `python-multipart`

## How GPU runner Dockerfiles inherit it

```dockerfile
ARG BASE_IMAGE=tztcloud/python-gpu-runner-base:v1.1.0
FROM ${BASE_IMAGE} AS builder

RUN pip install --no-cache-dir \
    torch torchaudio \
    --index-url https://download.pytorch.org/whl/cu128

COPY pyproject.toml ./
COPY src ./src
RUN pip install --no-cache-dir -e .

FROM ${BASE_IMAGE} AS runtime
RUN apt-get update && apt-get install -y --no-install-recommends ffmpeg
COPY --from=builder /opt/venv /opt/venv
```

Each runner still owns its model-specific torch extras and runtime OS
packages; the shared base only carries the common Python server stack.

Media-heavy siblings such as `openai-audio-runner` and `openai-tts-runner`
can layer on [`../python-gpu-media-runner-base/`](../python-gpu-media-runner-base/)
to avoid repeating the shared `ffmpeg` payload.

## Build

```bash
docker build -t tztcloud/python-gpu-runner-base:v1.1.0 .
```

Or from the parent component:

```bash
make -C .. gpu-base
```
