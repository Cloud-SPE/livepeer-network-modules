ARG REGISTRY=tztcloud
ARG TAG=v1.3.0
ARG BASE_IMAGE=${REGISTRY}/python-gpu-runner-base:${TAG}
FROM ${BASE_IMAGE}

RUN apt-get update && apt-get install -y --no-install-recommends \
    ffmpeg \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

EXPOSE 8080
