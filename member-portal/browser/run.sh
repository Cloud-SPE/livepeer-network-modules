#!/usr/bin/env bash
set -euo pipefail
portal_repo_root="$(cd "$(dirname "$0")/../.." && pwd)"
portal_evidence_dir="${PORTAL_BROWSER_EVIDENCE:-$(mktemp -d /tmp/regional-portal-browser.XXXXXX)}"
mkdir -p "$portal_evidence_dir"
if [[ -e "$portal_evidence_dir/fixture.json" ]]; then printf "Choose a fresh evidence directory.\n" >&2; exit 1; fi
portal_fixture_name="regional-portal-browser-$$"
docker run --rm -v "$portal_repo_root/member-portal/browser:/tests" -w /tests node:24-bookworm-slim npm ci --ignore-scripts
trap 'docker stop "$portal_fixture_name" >/dev/null 2>&1 || true' EXIT
docker run --rm --name "$portal_fixture_name" --network host \
  -e PORTAL_BROWSER_FIXTURE=/evidence/fixture.json \
  -v "$portal_evidence_dir:/evidence" -v "$portal_repo_root:/src" \
  -v regional-go-mod:/go/pkg/mod -v regional-go-cache:/root/.cache/go-build \
  -w /src/member-portal golang:1.26 \
  go test -race ./... -count=1 -coverprofile=/evidence/coverage.out -timeout=10m \
  >"$portal_evidence_dir/go-tests.log" 2>&1 &
portal_fixture_pid=$!
for ((attempt=0; attempt<150; attempt++)); do
  if [[ -s "$portal_evidence_dir/fixture.json" ]]; then break; fi
  if ! kill -0 "$portal_fixture_pid" 2>/dev/null; then cat "$portal_evidence_dir/go-tests.log"; exit 1; fi
  sleep 0.2
done
[[ -s "$portal_evidence_dir/fixture.json" ]]
docker run --rm --network host \
  -v "$portal_repo_root/member-portal/browser:/tests" -v "$portal_evidence_dir:/evidence" \
  -w /tests mcr.microsoft.com/playwright:v1.56.1-noble node acceptance.mjs \
  | tee "$portal_evidence_dir/browser-tests.log"
wait "$portal_fixture_pid"
printf 'Local browser evidence: %s\n' "$portal_evidence_dir"
