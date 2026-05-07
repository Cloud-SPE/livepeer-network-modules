# syntax=docker/dockerfile:1
#
# gateway-adapters/go test/build image. Used for `make test` (compile +
# go test ./...). The package ships as a Go module imported by gateway
# operators; this Dockerfile is the test/build environment per core
# belief #15.
#
# Build context is the monorepo root because the go.mod has a
# `replace` directive pointing at `../../livepeer-network-protocol/proto-go`.

ARG GO_VERSION=1.25

FROM golang:${GO_VERSION}-alpine AS test

WORKDIR /src

COPY gateway-adapters/go/go.mod gateway-adapters/go/go.sum* ./gateway-adapters/go/
COPY livepeer-network-protocol/proto-go/ ./livepeer-network-protocol/proto-go/
RUN cd gateway-adapters/go && go mod download

COPY gateway-adapters/go/ ./gateway-adapters/go/

WORKDIR /src/gateway-adapters/go
ENTRYPOINT ["go", "test", "-race", "-count=1", "./..."]
