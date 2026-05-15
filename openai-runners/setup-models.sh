#!/usr/bin/env bash
set -euo pipefail

# Per-host setup: download model weights and pre-compile Triton/Inductor
# kernels so the runners start fast. Safe to re-run; already-downloaded
# models are skipped and existing kernel caches are reused.
#
# Usage:
#   ./setup-models.sh                                              # default RealVisXL
#   MODEL_IDS="black-forest-labs/FLUX.1-dev" ./setup-models.sh     # use FLUX.1-dev instead
#   HF_TOKEN=hf_xxx ./setup-models.sh                              # for gated models
#
# Environment:
#   REGISTRY  Docker registry prefix (default: tztcloud)
#   TAG       Image tag (default: v1.1.0)

REGISTRY="${REGISTRY:-tztcloud}"
TAG="${TAG:-v1.1.0}"

DOWNLOADER_IMAGE="${REGISTRY}/image-model-downloader:${TAG}"
RUNNER_IMAGE="${REGISTRY}/openai-image-generation-runner:${TAG}"

MODEL_IDS="${MODEL_IDS:-SG161222/RealVisXL_V4.0_Lightning}"
MODEL_DIR="/models"
DEVICE="${DEVICE:-cuda}"
DTYPE="${DTYPE:-float16}"
HF_TOKEN="${HF_TOKEN:-}"

echo "==> Creating Docker volumes (if needed)"
docker volume create ai-image-models 2>/dev/null || true
docker volume create ai-image-kernel-cache 2>/dev/null || true
echo ""

echo "==> Downloading image model weights: ${MODEL_IDS}"

HF_TOKEN_ARGS=()
if [ -n "${HF_TOKEN}" ]; then
  HF_TOKEN_ARGS=(-e "HF_TOKEN=${HF_TOKEN}")
fi

docker run --rm \
  -v ai-image-models:${MODEL_DIR} \
  -e "MODEL_IDS=${MODEL_IDS}" \
  -e "MODEL_DIR=${MODEL_DIR}" \
  "${HF_TOKEN_ARGS[@]+"${HF_TOKEN_ARGS[@]}"}" \
  "${DOWNLOADER_IMAGE}"

echo ""
echo "==> Image model download complete."
echo ""

echo "==> Pre-compiling Triton kernels for each image model..."
IFS=',' read -ra MODELS <<< "${MODEL_IDS}"
for model_id in "${MODELS[@]}"; do
  model_id=$(echo "${model_id}" | xargs)
  echo "==> Warming up kernels for: ${model_id}"

  docker run --rm --gpus all \
    -v ai-image-models:${MODEL_DIR} \
    -v ai-image-kernel-cache:/cache \
    -e "WARMUP_ONLY=true" \
    -e "MODEL_ID=${model_id}" \
    -e "MODEL_DIR=${MODEL_DIR}" \
    -e "DEVICE=${DEVICE}" \
    -e "DTYPE=${DTYPE}" \
    -e "USE_TORCH_COMPILE=true" \
    "${RUNNER_IMAGE}"

  echo "==> Kernel warmup complete for: ${model_id}"
done

echo "============================================"
echo "  Setup complete!"
echo "  Image models downloaded: ${MODEL_IDS}"
echo "============================================"
