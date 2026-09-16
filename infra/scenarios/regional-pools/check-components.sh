#!/usr/bin/env bash
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
evidence="${1:-$(mktemp -d /tmp/regional-component-checks.XXXXXX)}"
mkdir -p "$evidence"
evidence="$(cd "$evidence" && pwd)"
for component in pool-commons pool-controller pool-member-agent member-portal pool-reconciler pool-payout-executor capability-broker payment-daemon protocol-daemon orch-coordinator service-registry-daemon; do
  printf 'Testing %s\n' "$component"
  docker run --rm --mount "type=bind,src=$root,dst=/src" \
    -v regional-go-mod:/go/pkg/mod -v regional-go-cache:/root/.cache/go-build \
    -w "/src/$component" golang:1.26 go test -race ./... \
    >"$evidence/$component.log" 2>&1
done
docker build -t regional-local/checks:validation -f "$root/infra/scenarios/regional-pools/Dockerfile.checks" "$root/infra/scenarios/regional-pools" >"$evidence/check-toolchain-build.log" 2>&1
docker run --rm --mount "type=bind,src=$root,dst=/src" \
  -v regional-go-mod:/go/pkg/mod -v regional-go-cache:/root/.cache/go-build \
  -w /src/livepeer-network-protocol regional-local/checks:validation make check \
  >"$evidence/protocol-check.log" 2>&1
docker run --rm --mount "type=bind,src=$root,dst=/src" \
  -v regional-go-mod:/go/pkg/mod -v regional-go-cache:/root/.cache/go-build \
  -w /src/livepeer-network-protocol/conformance golang:1.26 make conformance \
  >"$evidence/protocol-conformance.log" 2>&1
printf 'Local component and protocol checks passed: %s\n' "$evidence"
