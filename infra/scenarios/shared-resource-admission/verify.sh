#!/usr/bin/env bash
set -euo pipefail

scenario_dir="$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)"
repo_dir="$(CDPATH= cd -- "$scenario_dir/../../.." && pwd)"
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
bundle="${EVIDENCE_DIR:-$scenario_dir/run/evidence-$stamp}"
go_image="${GO_TEST_IMAGE:-golang:1.25.7-bookworm}"

for command in docker git sha256sum; do
  command -v "$command" >/dev/null || {
    echo "required command not found: $command" >&2
    exit 2
  }
done

umask 077
mkdir -p "$bundle"
exec > >(tee "$bundle/verification.log") 2>&1

run() {
  printf '\n$'
  printf ' %q' "$@"
  printf '\n'
  "$@"
}

container_go() {
  local workdir="$1"
  shift
  run docker run --rm \
    -v "$repo_dir:/src:ro" \
    -v livepeer-modules-go-cache:/root/.cache/go-build \
    -v livepeer-modules-go-mod:/go/pkg/mod \
    -w "/src/$workdir" \
    "$go_image" "$@"
}

modules_sha="$(git -C "$repo_dir" rev-parse HEAD)"
modules_dirty=false
[[ -z "$(git -C "$repo_dir" status --porcelain --untracked-files=normal)" ]] || modules_dirty=true

{
  printf 'observed_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'modules_sha=%s\n' "$modules_sha"
  printf 'modules_dirty=%s\n' "$modules_dirty"
  printf 'go_test_image=%s\n' "$go_image"
  printf 'host=%s\n' "$(hostname)"
} > "$bundle/metadata.env"

if command -v nvidia-smi >/dev/null; then
  run nvidia-smi --query-gpu=uuid,name,driver_version --format=csv,noheader \
    | tee "$bundle/gpu-inventory.csv"
else
  printf 'nvidia-smi unavailable\n' | tee "$bundle/gpu-inventory.txt"
fi

fixture="$repo_dir/pool-controller/testdata/shared-gpu-compose.yaml"
cp "$fixture" "$bundle/shared-gpu-compose.yaml"
sha256sum "$bundle/shared-gpu-compose.yaml" > "$bundle/shared-gpu-compose.sha256"
run docker compose -f "$fixture" config --quiet

container_go pool-controller go test ./internal/desiredstate \
  -run 'TestBuildSharesAdmissionDomainByPhysicalGPUAndIsolatesDifferentGPUs|TestTranscodeSharedGPUComposeGolden' \
  -count=1 -v

container_go pool-member-agent go test ./internal/desiredstate \
  -run 'TestPrepareHostAdmissions|TestApplyFailsClosedBeforeComposeWhenAdmissionPreparationFails' \
  -count=1 -v

container_go capability-broker go test ./internal/server ./internal/sessionengine ./internal/certification \
  -run 'TestRunnerCapacity|TestJobCapacityRefusal|TestSessionOpenNormalizesCapacity|TestOpenCapacityRefusal|TestCapacityRefusal|TestSessionCertificationCapacity' \
  -count=1 -v

container_go livepeer-network-protocol/conformance go run ./cmd/livepeer-conformance

if [[ -n "${TRANSCODE_REPO:-}" ]]; then
  transcode_dir="$(CDPATH= cd -- "$TRANSCODE_REPO" && pwd)"
  transcode_sha="$(git -C "$transcode_dir" rev-parse HEAD)"
  transcode_dirty=false
  [[ -z "$(git -C "$transcode_dir" status --porcelain --untracked-files=normal)" ]] || transcode_dirty=true
  {
    printf 'transcode_sha=%s\n' "$transcode_sha"
    printf 'transcode_dirty=%s\n' "$transcode_dirty"
  } >> "$bundle/metadata.env"

  transcode_go() {
    local module="$1"
    shift
    run docker run --rm \
      -v "$transcode_dir:/consumer:ro" \
      -v livepeer-modules-go-cache:/root/.cache/go-build \
      -v livepeer-modules-go-mod:/go/pkg/mod \
      -w "/consumer/$module" \
      "$go_image" "$@"
  }
  transcode_go transcode-core go test ./... -run GPUAdmission -count=1 -v
  transcode_go transcode-runner go test ./... -run SharedGPUAdmission -count=1 -v
  transcode_go abr-runner go test ./... -run SharedGPUAdmission -count=1 -v
  transcode_go live-runner go test ./... -run GPUAdmission -count=1 -v
fi

sha256sum "$bundle/metadata.env" "$bundle/shared-gpu-compose.yaml" \
  > "$bundle/SHA256SUMS"
printf '\nPASS: evidence bundle %s\n' "$bundle"
