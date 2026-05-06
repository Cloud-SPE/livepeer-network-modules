# syntax=docker/dockerfile:1
#
# Conformance runner image. Distroless final stage; fixtures baked in.
# Image: tztcloud/livepeer-conformance:<spec-VERSION>
#
# Build context is the monorepo root because the runner's go.mod has a
# `replace` directive pointing at `../../proto-go`. The proto-go module
# must be visible to the build alongside the runner module.
#
# Per core belief #16, the GO_VERSION ARG is bumped to latest stable on each
# release. Renovate / dependabot tracks this.

ARG GO_VERSION=1.25

# ---- Builder stage ----
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /src

# Copy module files first for layer caching.
COPY livepeer-network-protocol/conformance/runner/go.mod livepeer-network-protocol/conformance/runner/go.sum \
     ./livepeer-network-protocol/conformance/runner/
COPY livepeer-network-protocol/proto-go/ ./livepeer-network-protocol/proto-go/
RUN cd livepeer-network-protocol/conformance/runner && go mod download

# Copy source and build.
COPY livepeer-network-protocol/conformance/runner/ ./livepeer-network-protocol/conformance/runner/

ARG VERSION=dev
RUN cd livepeer-network-protocol/conformance/runner && CGO_ENABLED=0 GOOS=linux \
    go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/livepeer-conformance \
        ./cmd/livepeer-conformance

# ---- Runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/livepeer-conformance /usr/local/bin/livepeer-conformance
COPY livepeer-network-protocol/conformance/fixtures/ /fixtures/

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/livepeer-conformance"]
CMD ["--help"]
