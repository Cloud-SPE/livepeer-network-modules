#!/usr/bin/env bash
set -euo pipefail

# Multi-image build orchestrator for the openai-runners component.
#
# Environment:
#   REGISTRY        Docker registry prefix (default: tztcloud)
#   TAG             Image tag (default: from infra/build/image-versions.env)
#   PUSH            Push images after build (default: false)
#   PLATFORMS       Buildx platforms (default: linux/amd64; openai-runner ships multi-arch per OQ4)

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION_ENV_FILE="${ROOT}/infra/build/image-versions.env"
if [[ -f "$VERSION_ENV_FILE" ]]; then
  # shellcheck disable=SC1090
  . "$VERSION_ENV_FILE"
fi

REGISTRY="${REGISTRY:-tztcloud}"
TAG="${TAG:-${IMAGE_TAG_DEFAULT:-v1.3.0}}"
PUSH="${PUSH:-false}"
PLATFORMS="${PLATFORMS:-linux/amd64}"

cmd="${1:-build}"

base_image="${REGISTRY}/python-runner-base:${TAG}"
gpu_base_image="${REGISTRY}/python-gpu-runner-base:${TAG}"
gpu_media_base_image="${REGISTRY}/python-gpu-media-runner-base:${TAG}"

build_base() {
  echo "==> Building ${base_image}"
  docker build -t "${base_image}" -f python-runner-base/Dockerfile python-runner-base
}

build_gpu_base() {
  echo "==> Building ${gpu_base_image}"
  docker build -t "${gpu_base_image}" -f python-gpu-runner-base/Dockerfile python-gpu-runner-base
}

build_gpu_media_base() {
  echo "==> Building ${gpu_media_base_image} (FROM ${gpu_base_image})"
  docker build \
    --build-arg "REGISTRY=${REGISTRY}" \
    --build-arg "TAG=${TAG}" \
    --build-arg "BASE_IMAGE=${gpu_base_image}" \
    -t "${gpu_media_base_image}" \
    -f python-gpu-media-runner-base/Dockerfile \
    python-gpu-media-runner-base
}

build_chat_runner() {
  image="${REGISTRY}/openai-chat-runner:${TAG}"
  echo "==> Building ${image} (platforms ${PLATFORMS})"
  docker buildx build \
    --platform "${PLATFORMS}" \
    --build-arg "GO_VERSION=${GO_VERSION:-1.25.7}" \
    --build-arg "ALPINE_VERSION=${ALPINE_VERSION:-3.20}" \
    -t "${image}" \
    --load \
    -f openai-chat-runner/Dockerfile \
    openai-chat-runner
}

build_embeddings_runner() {
  image="${REGISTRY}/openai-embeddings-runner:${TAG}"
  echo "==> Building ${image} (platforms ${PLATFORMS})"
  docker buildx build \
    --platform "${PLATFORMS}" \
    --build-arg "GO_VERSION=${GO_VERSION:-1.25.7}" \
    --build-arg "ALPINE_VERSION=${ALPINE_VERSION:-3.20}" \
    -t "${image}" \
    --load \
    -f openai-embeddings-runner/Dockerfile \
    openai-embeddings-runner
}

build_python_runner() {
  local dir="$1"
  local image_suffix="$2"
  local base_image="$3"
  image="${REGISTRY}/${image_suffix}:${TAG}"
  echo "==> Building ${image} (FROM ${base_image})"
  docker build \
    --build-arg "REGISTRY=${REGISTRY}" \
    --build-arg "TAG=${TAG}" \
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
    build_gpu_base
    build_gpu_media_base
    build_chat_runner
    build_embeddings_runner
    build_python_runner openai-audio-runner openai-audio-runner "${gpu_media_base_image}"
    build_python_runner openai-tts-runner openai-tts-runner "${gpu_media_base_image}"
    build_python_runner openai-image-generation-runner openai-image-generation-runner "${gpu_base_image}"
    build_downloader
    build_tester
    echo "All images built successfully."
    ;;
  base)
    build_base
    ;;
  gpu-base)
    build_gpu_base
    ;;
  gpu-media-base)
    build_gpu_media_base
    ;;
  smoke)
    echo "==> Validating compose snippets"
    shopt -s nullglob
    snippets=(compose/docker-compose.*.yml)
    if [ ${#snippets[@]} -eq 0 ]; then
      echo "no compose snippets found under compose/" >&2
      exit 1
    fi
    for f in "${snippets[@]}"; do
      echo "  - $f"
      docker compose -f "$f" config >/dev/null
    done
    echo "All compose snippets valid (${#snippets[@]} file(s))"
    ;;
  *)
    echo "usage: build.sh [build|base|gpu-base|gpu-media-base|smoke]" >&2
    exit 2
    ;;
esac

if [ "${PUSH}" = "true" ]; then
  echo "Pushing images..."
  docker push "${base_image}"
  docker push "${gpu_base_image}"
  docker push "${gpu_media_base_image}"
  for image in \
    "${REGISTRY}/openai-chat-runner:${TAG}" \
    "${REGISTRY}/openai-embeddings-runner:${TAG}" \
    "${REGISTRY}/openai-audio-runner:${TAG}" \
    "${REGISTRY}/openai-tts-runner:${TAG}" \
    "${REGISTRY}/openai-image-generation-runner:${TAG}" \
    "${REGISTRY}/image-model-downloader:${TAG}" \
    "${REGISTRY}/openai-tester:${TAG}"
  do
    docker push "${image}"
  done
fi
