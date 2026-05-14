FROM nvidia/cuda:12.9.1-runtime-ubuntu24.04

ENV DEBIAN_FRONTEND=noninteractive
ENV PYTHONUNBUFFERED=1
ENV PIP_NO_CACHE_DIR=1
ENV PIP_DISABLE_PIP_VERSION_CHECK=1
ENV VIRTUAL_ENV=/opt/venv
ENV PATH=/opt/venv/bin:$PATH

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    python3 \
    python3-pip \
    python3-venv \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

RUN python3 -m venv "$VIRTUAL_ENV" \
    && pip install --upgrade pip setuptools wheel \
    && rm -rf /root/.cache

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
