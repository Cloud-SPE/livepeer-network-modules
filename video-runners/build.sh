#!/usr/bin/env bash
set -euo pipefail

# Build orchestrator for the video-runners component.
#
# Environment:
#   REGISTRY      Docker registry prefix (default: tztcloud)
#   TAG           Image tag (default: from infra/build/image-versions.env)
#   PUSH          Push images after build (default: false)

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION_ENV_FILE="${ROOT}/infra/build/image-versions.env"
if [[ -f "$VERSION_ENV_FILE" ]]; then
  # shellcheck disable=SC1090
  . "$VERSION_ENV_FILE"
fi

REGISTRY="${REGISTRY:-tztcloud}"
TAG="${TAG:-${IMAGE_TAG_DEFAULT:-v1.2.0}}"
PUSH="${PUSH:-false}"
CODECS_IMAGE="${CODECS_IMAGE:-${REGISTRY}/codecs-builder:${TAG}}"

cmd="${1:-build}"

build_codecs() {
  echo "==> Building ${CODECS_IMAGE}"
  docker build -t "${CODECS_IMAGE}" -f codecs-builder/Dockerfile codecs-builder
}

build_transcode() {
  image="${REGISTRY}/transcode-runner:${TAG}"
  echo "==> Building ${image} (FROM ${CODECS_IMAGE})"
  docker build \
    --build-arg "REGISTRY=${REGISTRY}" \
    --build-arg "TAG=${TAG}" \
    --build-arg "CUDA_VERSION=${CUDA_VERSION:-12.9.1}" \
    --build-arg "UBUNTU_VERSION=${UBUNTU_VERSION:-24.04}" \
    --build-arg "GO_VERSION=${GO_VERSION:-1.25.7}" \
    --build-arg "CODECS_IMAGE=${CODECS_IMAGE}" \
    -t "${image}" \
    -f transcode-runner/Dockerfile \
    .
}

build_abr() {
  image="${REGISTRY}/abr-runner:${TAG}"
  echo "==> Building ${image} (FROM ${CODECS_IMAGE})"
  docker build \
    --build-arg "REGISTRY=${REGISTRY}" \
    --build-arg "TAG=${TAG}" \
    --build-arg "CUDA_VERSION=${CUDA_VERSION:-12.9.1}" \
    --build-arg "UBUNTU_VERSION=${UBUNTU_VERSION:-24.04}" \
    --build-arg "GO_VERSION=${GO_VERSION:-1.25.7}" \
    --build-arg "CODECS_IMAGE=${CODECS_IMAGE}" \
    -t "${image}" \
    -f abr-runner/Dockerfile \
    .
}

build_tester() {
  image="${REGISTRY}/transcode-tester:${TAG}"
  echo "==> Building ${image}"
  docker build -t "${image}" -f transcode-tester/Dockerfile transcode-tester
}

case "${cmd}" in
  build)
    build_codecs
    build_transcode
    build_abr
    build_tester
    echo "All images built successfully."
    ;;
  codecs)
    build_codecs
    ;;
  smoke)
    echo "==> Compose config validation"
    docker compose -f compose/docker-compose.yml config >/dev/null
    echo "compose config valid"
    ;;
  *)
    echo "usage: build.sh [build|codecs|smoke]" >&2
    exit 2
    ;;
esac

if [ "${PUSH}" = "true" ]; then
  echo "Pushing images..."
  for image in \
    "${CODECS_IMAGE}" \
    "${REGISTRY}/transcode-runner:${TAG}" \
    "${REGISTRY}/abr-runner:${TAG}" \
    "${REGISTRY}/transcode-tester:${TAG}"
  do
    docker push "${image}"
  done
fi
