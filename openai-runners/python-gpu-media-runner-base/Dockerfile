ARG BASE_IMAGE=tztcloud/python-gpu-runner-base:v1.1.0
FROM ${BASE_IMAGE}

RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

EXPOSE 8080
