FROM nvidia/cuda:12.9.1-devel-ubuntu24.04 AS gpu-base

ENV DEBIAN_FRONTEND=noninteractive
ENV PYTHONUNBUFFERED=1
ENV PIP_NO_CACHE_DIR=1
ENV PIP_DISABLE_PIP_VERSION_CHECK=1

RUN apt-get update && apt-get install -y --no-install-recommends \
    python3 \
    python3-pip \
    git \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

RUN pip3 install --no-cache-dir --break-system-packages \
    "fastapi>=0.115.0,<0.117.0" \
    "pydantic>=2.9.0,<3.0.0" \
    "pydantic-settings>=2.5.0,<3.0.0" \
    "structlog>=24.4.0,<25.0.0" \
    "uvicorn[standard]>=0.30.0,<0.33.0" \
    "prometheus-client>=0.21.0,<0.22.0" \
    "python-multipart>=0.0.12,<0.1.0" \
    && rm -rf /root/.cache

RUN pip3 install --no-cache-dir --break-system-packages \
    torch torchvision torchaudio \
    --index-url https://download.pytorch.org/whl/cu128 \
    && rm -rf /root/.cache

WORKDIR /app
COPY pyproject.toml ./
COPY src ./src
RUN pip3 install --no-cache-dir --break-system-packages -e . \
    && rm -rf /root/.cache

COPY offering.yaml /etc/runner/offering.yaml

VOLUME /models

ENV CAPABILITY_NAME=image-generation
ENV MODEL_DIR=/models
ENV RUNNER_PORT=8080
ENV DEVICE=cuda
ENV DTYPE=float16
ENV MAX_QUEUE_SIZE=5
ENV USE_TORCH_COMPILE=false
ENV METRICS_ENABLED=false

ENV TRITON_CACHE_DIR=/cache/triton
ENV TORCHINDUCTOR_FX_GRAPH_CACHE=1
ENV TORCHINDUCTOR_CACHE_DIR=/cache/inductor

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --retries=3 \
    CMD python3 -c "import urllib.request; urllib.request.urlopen('http://localhost:8080/healthz')" || exit 1

CMD ["python3", "-m", "openai_image_generation_runner"]
