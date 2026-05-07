#!/usr/bin/env bash
set -euo pipefail

# Multi-image build orchestrator for the openai-runners component.
#
# Environment:
#   REGISTRY        Docker registry prefix (default: tztcloud)
#   TAG             Image tag (default: v0.8.10 — frozen per user-memory feedback_no_image_version_bumps.md)
#   PUSH            Push images after build (default: false)
#   PLATFORMS       Buildx platforms (default: linux/amd64; openai-runner ships multi-arch per OQ4)

REGISTRY="${REGISTRY:-tztcloud}"
TAG="${TAG:-v0.8.10}"
PUSH="${PUSH:-false}"
PLATFORMS="${PLATFORMS:-linux/amd64}"

cmd="${1:-build}"

base_image="${REGISTRY}/python-runner-base:${TAG}"

build_base() {
  echo "==> Building ${base_image}"
  docker build -t "${base_image}" -f python-runner-base/Dockerfile python-runner-base
}

build_go_runner() {
  for target in chat embeddings; do
    image="${REGISTRY}/openai-runner-${target}:${TAG}"
    echo "==> Building ${image} (target ${target}, platforms ${PLATFORMS})"
    docker buildx build \
      --platform "${PLATFORMS}" \
      --target "${target}" \
      -t "${image}" \
      --load \
      -f openai-runner/Dockerfile \
      openai-runner
  done
}

build_python_runner() {
  local dir="$1"
  local image_suffix="$2"
  image="${REGISTRY}/${image_suffix}:${TAG}"
  echo "==> Building ${image} (FROM ${base_image})"
  docker build \
    --build-arg "BASE_IMAGE=${base_image}" \
    -t "${image}" \
    -f "${dir}/Dockerfile" \
    "${dir}"
}

build_downloader() {
  image="${REGISTRY}/image-model-downloader:${TAG}"
  echo "==> Building ${image}"
  docker build -t "${image}" -f image-model-downloader/Dockerfile image-model-downloader
}

build_tester() {
  image="${REGISTRY}/openai-tester:${TAG}"
  echo "==> Building ${image}"
  docker build -t "${image}" -f openai-tester/Dockerfile openai-tester
}

case "${cmd}" in
  build)
    build_base
    build_go_runner
    build_python_runner openai-audio-runner openai-audio-runner
    build_python_runner openai-tts-runner openai-tts-runner
    build_python_runner openai-image-generation-runner openai-image-generation-runner
    build_downloader
    build_tester
    echo "All images built successfully."
    ;;
  base)
    build_base
    ;;
  smoke)
    echo "==> Cross-runner smoke against compose stack"
    docker compose -f compose/docker-compose.yml config >/dev/null
    echo "compose config valid"
    ;;
  *)
    echo "usage: build.sh [build|base|smoke]" >&2
    exit 2
    ;;
esac

if [ "${PUSH}" = "true" ]; then
  echo "Pushing images..."
  docker push "${base_image}"
  for image in \
    "${REGISTRY}/openai-runner-chat:${TAG}" \
    "${REGISTRY}/openai-runner-embeddings:${TAG}" \
    "${REGISTRY}/openai-audio-runner:${TAG}" \
    "${REGISTRY}/openai-tts-runner:${TAG}" \
    "${REGISTRY}/openai-image-generation-runner:${TAG}" \
    "${REGISTRY}/image-model-downloader:${TAG}" \
    "${REGISTRY}/openai-tester:${TAG}"
  do
    docker push "${image}"
  done
fi
