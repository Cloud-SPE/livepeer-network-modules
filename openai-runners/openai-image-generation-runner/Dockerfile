ARG BASE_IMAGE=tztcloud/python-gpu-runner-base:v1.0.0
FROM ${BASE_IMAGE} AS builder

RUN pip install --no-cache-dir \
    torch torchvision \
    --index-url https://download.pytorch.org/whl/cu128 \
    && rm -rf /root/.cache

WORKDIR /build
COPY pyproject.toml ./
COPY src ./src
RUN pip install --no-cache-dir . \
    && rm -rf /root/.cache

FROM ${BASE_IMAGE} AS runtime

COPY --from=builder /opt/venv /opt/venv

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
