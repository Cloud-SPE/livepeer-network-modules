ARG REGISTRY=tztcloud
ARG TAG=v1.2.0
ARG BASE_IMAGE=${REGISTRY}/python-gpu-runner-base:${TAG}
FROM ${BASE_IMAGE} AS builder

RUN pip install --no-cache-dir \
    torch \
    --index-url https://download.pytorch.org/whl/cu128 \
    && rm -rf /root/.cache

WORKDIR /build
COPY pyproject.toml ./
COPY src ./src
RUN pip install --no-cache-dir . \
    && python3 -c "from importlib.metadata import version; assert int(version('sentence-transformers').split('.', 1)[0]) < 5, version('sentence-transformers'); assert int(version('transformers').split('.', 1)[0]) < 5, version('transformers')" \
    && rm -rf /root/.cache

FROM ${BASE_IMAGE} AS runtime

COPY --from=builder /opt/venv /opt/venv

COPY offering.yaml /etc/runner/offering.yaml

VOLUME /models

ENV CAPABILITY_NAME=rerank
ENV MODEL_ID=zeroentropy/zerank-2
ENV MODEL_DIR=/models
ENV RUNNER_PORT=8080
ENV DEVICE=cuda
ENV DTYPE=bfloat16
ENV MAX_QUEUE_SIZE=5
ENV MAX_BATCH_SIZE=1000
ENV INFERENCE_BATCH_SIZE=64
ENV METRICS_ENABLED=false

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --retries=3 \
    CMD python3 -c "import urllib.request; urllib.request.urlopen('http://localhost:8080/healthz')" || exit 1

CMD ["python3", "-m", "rerank_runner"]
