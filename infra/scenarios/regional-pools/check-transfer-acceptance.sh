#!/usr/bin/env bash
# Local containers only: synthetic hardware/work, no chain calls or funds.
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
endpoint="${DOCKER_HOST:-$(docker context inspect --format '{{.Endpoints.docker.Host}}')}"
case "$endpoint" in
  unix://*) regional_socket="${endpoint#unix://}" ;;
  *) echo 'This acceptance test requires a local Unix Docker socket.' >&2; exit 1 ;;
esac
regional_docker="$(command -v docker)"
regional_compose="$(docker info --format '{{range .ClientInfo.Plugins}}{{if eq .Name "compose"}}{{.Path}}{{end}}{{end}}')"
test -n "$regional_compose"
docker run --rm --network host \
  -e REGIONAL_DOCKER_ACCEPTANCE=1 -e DOCKER_HOST=unix:///var/run/docker.sock \
  --mount "type=bind,src=$regional_socket,dst=/var/run/docker.sock" \
  --mount "type=bind,src=$regional_docker,dst=/usr/bin/docker,readonly" \
  --mount "type=bind,src=$(dirname "$regional_compose"),dst=/usr/libexec/docker/cli-plugins,readonly" \
  --mount "type=bind,src=$root,dst=/src" \
  -v regional-go-mod:/go/pkg/mod -v regional-go-cache:/root/.cache/go-build \
  -w /src/e2e golang:1.26 \
  go test -race -count=1 -run '^TestRegionalTransferWithActualAgentContainerStop$' -timeout 10m ./...
