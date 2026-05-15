ARG BASE_IMAGE=tztcloud/python-gpu-media-runner-base:v1.1.0
FROM ${BASE_IMAGE} AS builder

RUN apt-get update && apt-get install -y --no-install-recommends \
    espeak-ng \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir \
    torch torchaudio \
    --index-url https://download.pytorch.org/whl/cu128 \
    && rm -rf /root/.cache

WORKDIR /build
COPY pyproject.toml ./
COPY src ./src
RUN pip install --no-cache-dir . \
    && python3 -m spacy download en_core_web_sm \
    && rm -rf /root/.cache

FROM ${BASE_IMAGE} AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends \
    espeak-ng \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /opt/venv /opt/venv

COPY offering.yaml /etc/runner/offering.yaml

VOLUME /models

ENV CAPABILITY_NAME=openai-audio-speech
ENV MODEL_ID=hexgrad/Kokoro-82M
ENV MODEL_DIR=/models
ENV RUNNER_PORT=8080
ENV DEVICE=cuda
ENV MAX_QUEUE_SIZE=5
ENV LANG_CODE=a
ENV MAX_INPUT_CHARS=4000
ENV DEFAULT_VOICE=af_bella
ENV HF_HOME=/models/huggingface
ENV METRICS_ENABLED=false

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=10s --retries=3 \
    CMD python3 -c "import urllib.request; urllib.request.urlopen('http://localhost:8080/healthz')" || exit 1

CMD ["python3", "-m", "openai_tts_runner"]
