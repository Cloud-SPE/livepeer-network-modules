ARG BASE_IMAGE=tztcloud/python-gpu-media-runner-base:v1.0.0
FROM ${BASE_IMAGE} AS builder

RUN pip install --no-cache-dir \
    torch torchaudio \
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

ENV CAPABILITY_NAME=openai-audio-transcriptions
ENV MODEL_ID=openai/whisper-large-v3
ENV MODEL_DIR=/models
ENV RUNNER_PORT=8080
ENV DEVICE=cuda
ENV DTYPE=bfloat16
ENV MAX_QUEUE_SIZE=5
ENV MAX_AUDIO_MB=50
ENV CHUNK_LENGTH_S=30
ENV INFERENCE_BATCH_SIZE=16
ENV METRICS_ENABLED=false

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --retries=3 \
    CMD python3 -c "import urllib.request; urllib.request.urlopen('http://localhost:8080/healthz')" || exit 1

CMD ["python3", "-m", "openai_audio_runner"]
