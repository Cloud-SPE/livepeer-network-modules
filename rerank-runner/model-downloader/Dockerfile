FROM python:3.12-slim

ENV DEBIAN_FRONTEND=noninteractive
ENV PYTHONUNBUFFERED=1
ENV PIP_NO_CACHE_DIR=1
ENV PIP_DISABLE_PIP_VERSION_CHECK=1

RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY pyproject.toml ./
RUN pip install --no-cache-dir "huggingface_hub>=0.20.0" \
    && rm -rf /root/.cache

COPY src/download.py ./

VOLUME /models
ENV MODEL_DIR=/models
ENV MODEL_ID=zeroentropy/zerank-2

CMD ["python3", "download.py"]
