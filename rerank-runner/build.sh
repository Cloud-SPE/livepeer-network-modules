#!/usr/bin/env bash
set -euo pipefail

# Build orchestrator for the rerank-runner component.
#
# Environment:
#   REGISTRY      Docker registry prefix (default: tztcloud)
#   TAG           Image tag (default: v0.8.10 — frozen per user-memory feedback_no_image_version_bumps.md)
#   BASE_IMAGE    Python base image (default: ${REGISTRY}/python-runner-base:${TAG})
#   PUSH          Push images after build (default: false)

REGISTRY="${REGISTRY:-tztcloud}"
TAG="${TAG:-v0.8.10}"
BASE_IMAGE="${BASE_IMAGE:-${REGISTRY}/python-runner-base:${TAG}}"
PUSH="${PUSH:-false}"

cmd="${1:-build}"

build_runner() {
  image="${REGISTRY}/rerank-runner:${TAG}"
  echo "==> Building ${image} (FROM ${BASE_IMAGE})"
  docker build \
    --build-arg "BASE_IMAGE=${BASE_IMAGE}" \
    -t "${image}" \
    -f Dockerfile \
    .
}

build_downloader() {
  image="${REGISTRY}/rerank-model-downloader:${TAG}"
  echo "==> Building ${image}"
  docker build -t "${image}" -f model-downloader/Dockerfile model-downloader
}

case "${cmd}" in
  build)
    build_runner
    build_downloader
    echo "All images built successfully."
    ;;
  smoke)
    echo "==> Compose config validation"
    docker compose -f compose/docker-compose.yml config >/dev/null
    echo "compose config valid"
    ;;
  *)
    echo "usage: build.sh [build|smoke]" >&2
    exit 2
    ;;
esac

if [ "${PUSH}" = "true" ]; then
  echo "Pushing images..."
  for image in \
    "${REGISTRY}/rerank-runner:${TAG}" \
    "${REGISTRY}/rerank-model-downloader:${TAG}"
  do
    docker push "${image}"
  done
fi
