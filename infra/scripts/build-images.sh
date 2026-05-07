#!/usr/bin/env bash
# Build all Cloud-SPE Docker images in dependency order.
#
# Usage:
#   ./infra/scripts/build-images.sh                 # build everything
#   ./infra/scripts/build-images.sh capability-broker payment-daemon
#                                                    # build a subset (substring match)
#
# Env:
#   REGISTRY  default: tztcloud
#   TAG       default: v1.0.0
#   PUSH      set to 1 to docker push after each build
#
# Notes:
#   - Run from the monorepo root.
#   - Tier 0 base images (codecs-builder, python-runner-base) are built
#     first; downstream multi-arch video runners FROM the codecs image.
#   - Multi-target Dockerfiles (openai-runner: chat+embeddings; video
#     transcode/abr: nvidia+intel+amd) are expanded into multiple builds.

set -euo pipefail

REGISTRY="${REGISTRY:-tztcloud}"
TAG="${TAG:-v1.0.0}"
PUSH="${PUSH:-0}"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

# ---- helpers --------------------------------------------------------------

step=0
total=0

log()      { printf '\033[1;34m[build]\033[0m %s\n' "$*" >&2; }
ok()       { printf '\033[1;32m[ ok ]\033[0m %s\n' "$*" >&2; }
warn()     { printf '\033[1;33m[warn]\033[0m %s\n' "$*" >&2; }
fail()     { printf '\033[1;31m[fail]\033[0m %s\n' "$*" >&2; exit 1; }

# Each entry: "name|context|dockerfile|target_or_empty|extra_build_args_or_empty"
# Order matters: tier 0 first, then independent components, then images
# that depend on tier 0 outputs.
declare -a IMAGES=(
  # Tier 0 — base images consumed by downstream FROMs / BASE_IMAGE args
  "codecs-builder|video-runners/codecs-builder|video-runners/codecs-builder/Dockerfile||"
  "python-runner-base|openai-runners/python-runner-base|openai-runners/python-runner-base/Dockerfile||"

  # Tier 1 — Go services, monorepo-root context (proto-go replace dirs)
  "livepeer-capability-broker|.|capability-broker/Dockerfile||"
  "livepeer-payment-daemon|.|payment-daemon/Dockerfile||"
  "livepeer-orch-coordinator|.|orch-coordinator/Dockerfile||"
  "livepeer-secure-orch-console|.|secure-orch-console/Dockerfile||"
  "livepeer-gateway-adapters-go|.|gateway-adapters/go/Dockerfile||"
  "livepeer-conformance|.|livepeer-network-protocol/conformance/Dockerfile||"
  "livepeer-conformance-session-runner|.|livepeer-network-protocol/conformance/runner/session-runner-stub/Dockerfile||"

  # Tier 1 — Go workload runners (multi-target Dockerfile)
  "openai-runner-chat|openai-runners/openai-runner|openai-runners/openai-runner/Dockerfile|chat|"
  "openai-runner-embeddings|openai-runners/openai-runner|openai-runners/openai-runner/Dockerfile|embeddings|"

  # Tier 2 — Node SaaS gateways (monorepo-root context, pnpm workspace)
  "livepeer-customer-portal|customer-portal|customer-portal/Dockerfile||"
  "livepeer-gateway-adapters-ts|gateway-adapters/ts|gateway-adapters/ts/Dockerfile||"
  "livepeer-openai-gateway-reference|.|openai-gateway/Dockerfile||"
  "livepeer-video-gateway|.|video-gateway/Dockerfile||"
  "livepeer-vtuber-gateway|.|vtuber-gateway/Dockerfile||"

  # Tier 3 — Python lightweight (model downloaders + test fixtures)
  "image-model-downloader|openai-runners/image-model-downloader|openai-runners/image-model-downloader/Dockerfile||"
  "rerank-model-downloader|rerank-runner/model-downloader|rerank-runner/model-downloader/Dockerfile||"
  "openai-gateway-mock-runner|openai-gateway/test/mock-runner|openai-gateway/test/mock-runner/Dockerfile||"

  # Tier 4 — Test helpers
  "openai-tester|openai-runners/openai-tester|openai-runners/openai-tester/Dockerfile||"
  "transcode-tester|video-runners/transcode-tester|video-runners/transcode-tester/Dockerfile||"

  # Tier 5 — Heavy GPU/ML runners
  "vtuber-runner|.|vtuber-runner/Dockerfile||"
  "rerank-runner|rerank-runner|rerank-runner/Dockerfile||--build-arg=BASE_IMAGE=${REGISTRY}/python-runner-base:${TAG}"
  "openai-audio-runner|openai-runners/openai-audio-runner|openai-runners/openai-audio-runner/Dockerfile||--build-arg=BASE_IMAGE=${REGISTRY}/python-runner-base:${TAG}"
  "openai-image-generation-runner|openai-runners/openai-image-generation-runner|openai-runners/openai-image-generation-runner/Dockerfile||--build-arg=BASE_IMAGE=${REGISTRY}/python-runner-base:${TAG}"
  "openai-tts-runner|openai-runners/openai-tts-runner|openai-runners/openai-tts-runner/Dockerfile||--build-arg=BASE_IMAGE=${REGISTRY}/python-runner-base:${TAG}"

  # Tier 5 — Multi-arch video runners (codecs-builder consumed via build-arg)
  "abr-runner|video-runners|video-runners/abr-runner/Dockerfile|runtime-amd|--build-arg=CODECS_IMAGE=${REGISTRY}/codecs-builder:${TAG}"
  "abr-runner-nvidia|video-runners|video-runners/abr-runner/Dockerfile|runtime-nvidia|--build-arg=CODECS_IMAGE=${REGISTRY}/codecs-builder:${TAG}"
  "abr-runner-intel|video-runners|video-runners/abr-runner/Dockerfile|runtime-intel|--build-arg=CODECS_IMAGE=${REGISTRY}/codecs-builder:${TAG}"
  "transcode-runner|video-runners|video-runners/transcode-runner/Dockerfile|runtime-amd|--build-arg=CODECS_IMAGE=${REGISTRY}/codecs-builder:${TAG}"
  "transcode-runner-nvidia|video-runners|video-runners/transcode-runner/Dockerfile|runtime-nvidia|--build-arg=CODECS_IMAGE=${REGISTRY}/codecs-builder:${TAG}"
  "transcode-runner-intel|video-runners|video-runners/transcode-runner/Dockerfile|runtime-intel|--build-arg=CODECS_IMAGE=${REGISTRY}/codecs-builder:${TAG}"
)

# ---- filter ---------------------------------------------------------------

filter_args=("$@")
declare -a SELECTED
if [[ ${#filter_args[@]} -eq 0 ]]; then
  SELECTED=("${IMAGES[@]}")
else
  for entry in "${IMAGES[@]}"; do
    name="${entry%%|*}"
    for f in "${filter_args[@]}"; do
      if [[ "$name" == *"$f"* ]]; then
        SELECTED+=("$entry")
        break
      fi
    done
  done
  if [[ ${#SELECTED[@]} -eq 0 ]]; then
    fail "No images matched filter(s): ${filter_args[*]}"
  fi
fi

total=${#SELECTED[@]}

# ---- build loop -----------------------------------------------------------

log "registry=${REGISTRY}  tag=${TAG}  push=${PUSH}  building ${total} image(s)"

for entry in "${SELECTED[@]}"; do
  step=$((step + 1))
  IFS='|' read -r name context dockerfile target build_args <<<"$entry"

  full_tag="${REGISTRY}/${name}:${TAG}"

  args=(build -t "$full_tag" -f "$dockerfile")
  [[ -n "$target" ]]     && args+=(--target "$target")
  [[ -n "$build_args" ]] && args+=("$build_args")
  args+=("$context")

  log "[$step/$total] $full_tag"
  if ! docker "${args[@]}"; then
    fail "build failed for $full_tag"
  fi
  ok "[$step/$total] $full_tag"

  if [[ "$PUSH" == "1" ]]; then
    log "[$step/$total] pushing $full_tag"
    docker push "$full_tag" || fail "push failed for $full_tag"
    ok "[$step/$total] pushed $full_tag"
  fi
done

ok "all $total image(s) built (registry=${REGISTRY} tag=${TAG})"
