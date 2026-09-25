#!/bin/sh
set -eu
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
docker run --rm \
  -v "$repo_root:/src" -w /src \
  -v livepeer-go-modules:/go/pkg/mod -v livepeer-go-build:/root/.cache/go-build \
  golang:1.25 \
  sh -ec 'go -C payment-daemon test -race -c -o /tmp/session-receiver-fixture.test ./internal/service/receiver
    SESSION_RECEIVER_FIXTURE_BIN=/tmp/session-receiver-fixture.test go -C capability-broker test -race ./internal/sessionengine ./internal/sessionstore ./internal/payment ./internal/workledger ./internal/server
    go -C payment-daemon test -race ./internal/store ./internal/service/receiver'
