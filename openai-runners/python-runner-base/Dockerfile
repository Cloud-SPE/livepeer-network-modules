FROM python:3.12-slim AS base

ENV DEBIAN_FRONTEND=noninteractive
ENV PYTHONUNBUFFERED=1
ENV PIP_NO_CACHE_DIR=1
ENV PIP_DISABLE_PIP_VERSION_CHECK=1

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates \
        curl \
        ffmpeg \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY pyproject.toml ./
RUN pip install --no-cache-dir \
    "fastapi>=0.115.0,<0.117.0" \
    "pydantic>=2.9.0,<3.0.0" \
    "pydantic-settings>=2.5.0,<3.0.0" \
    "structlog>=24.4.0,<25.0.0" \
    "uvicorn[standard]>=0.30.0,<0.33.0" \
    "prometheus-client>=0.21.0,<0.22.0" \
    "python-multipart>=0.0.12,<0.1.0" \
    && rm -rf /root/.cache

EXPOSE 8080
