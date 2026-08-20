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
#   TAG       default: v2.0.0
#   VERSION   default: derived from git tag/sha for binary build metadata
#   PUSH      set to 1 to docker push after each build
#
# Notes:
#   - Run from the monorepo root.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$ROOT"

VERSION_ENV_FILE="${ROOT}/infra/build/image-versions.env"
if [[ -f "$VERSION_ENV_FILE" ]]; then
  # shellcheck disable=SC1090
  . "$VERSION_ENV_FILE"
fi

REGISTRY="${REGISTRY:-tztcloud}"
TAG="${TAG:-${IMAGE_TAG_DEFAULT:-v2.0.0}}"
PUSH="${PUSH:-0}"
DEFAULT_VERSION="$(VERSION_PREFIX="${TAG}" FALLBACK_VERSION="${TAG}" ./infra/build/git-version.sh)"
VERSION="${VERSION:-${DEFAULT_VERSION}}"

# ---- helpers --------------------------------------------------------------

step=0
total=0

GLOBAL_BUILD_ARGS=(
  "--build-arg=REGISTRY=${REGISTRY}"
  "--build-arg=TAG=${TAG}"
  "--build-arg=GO_VERSION=${GO_VERSION:-1.25.7}"
  "--build-arg=NODE_VERSION=${NODE_VERSION:-22}"
  "--build-arg=PYTHON_VERSION=${PYTHON_VERSION:-3.12}"
  "--build-arg=ALPINE_VERSION=${ALPINE_VERSION:-3.20}"
  "--build-arg=UBUNTU_VERSION=${UBUNTU_VERSION:-24.04}"
  "--build-arg=CUDA_VERSION=${CUDA_VERSION:-12.9.1}"
  "--build-arg=VERSION=${VERSION}"
)

log()      { printf '\033[1;34m[build]\033[0m %s\n' "$*" >&2; }
ok()       { printf '\033[1;32m[ ok ]\033[0m %s\n' "$*" >&2; }
warn()     { printf '\033[1;33m[warn]\033[0m %s\n' "$*" >&2; }
fail()     { printf '\033[1;31m[fail]\033[0m %s\n' "$*" >&2; exit 1; }
is_pushable_image() {
  case "$1" in
    livepeer-capability-broker|\
    livepeer-payment-daemon|\
    livepeer-protocol-daemon|\
    livepeer-service-registry-daemon|\
    livepeer-orch-coordinator|\
    livepeer-pool-controller|\
    livepeer-pool-member-agent|\
    livepeer-pool-reconciler|\
    livepeer-pool-payout-executor|\
    livepeer-secure-orch-console)
      return 0
      ;;
    *)
      return 1
      ;;
  esac
}

# Each entry: "name|context|dockerfile|target_or_empty|extra_build_args_or_empty"
# Order matters only for readability.
declare -a IMAGES=(
  # Go services, monorepo-root context (proto-go replace dirs)
  "livepeer-capability-broker|.|capability-broker/Dockerfile||"
  "livepeer-payment-daemon|.|payment-daemon/Dockerfile||"
  "livepeer-protocol-daemon|.|protocol-daemon/Dockerfile||"
  "livepeer-service-registry-daemon|.|service-registry-daemon/Dockerfile||"
  "livepeer-orch-coordinator|.|orch-coordinator/Dockerfile||"
  "livepeer-pool-controller|.|pool-controller/Dockerfile||"
  "livepeer-pool-member-agent|pool-member-agent|pool-member-agent/Dockerfile||"
  "livepeer-pool-reconciler|.|pool-reconciler/Dockerfile||"
  "livepeer-pool-payout-executor|.|pool-payout-executor/Dockerfile||"
  "livepeer-secure-orch-console|.|secure-orch-console/Dockerfile||"
  "livepeer-conformance|.|livepeer-network-protocol/conformance/Dockerfile||"
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

log "registry=${REGISTRY}  tag=${TAG}  version=${VERSION}  push=${PUSH}  building ${total} image(s)"

for entry in "${SELECTED[@]}"; do
  step=$((step + 1))
  IFS='|' read -r name context dockerfile target build_args <<<"$entry"

  full_tag="${REGISTRY}/${name}:${TAG}"

  args=(build -t "$full_tag" -f "$dockerfile")
  args+=("${GLOBAL_BUILD_ARGS[@]}")
  [[ -n "$target" ]]     && args+=(--target "$target")
  [[ -n "$build_args" ]] && args+=("$build_args")
  args+=("$context")

  log "[$step/$total] $full_tag"
  if ! docker "${args[@]}"; then
    fail "build failed for $full_tag"
  fi
  ok "[$step/$total] $full_tag"

  if [[ "$PUSH" == "1" ]]; then
    if ! is_pushable_image "$name"; then
      ok "[$step/$total] built local-only image; skipping push for $full_tag"
      continue
    fi
    log "[$step/$total] pushing $full_tag"
    docker push "$full_tag" || fail "push failed for $full_tag"
    ok "[$step/$total] pushed $full_tag"
  fi
done

ok "all $total image(s) built (registry=${REGISTRY} tag=${TAG})"
