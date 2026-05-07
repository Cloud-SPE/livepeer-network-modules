# syntax=docker/dockerfile:1
#
# Conformance stub session-runner image.
# Image: tztcloud/livepeer-conformance-session-runner:<spec-VERSION>
#
# Built from the monorepo root because the runner module references
# `../../proto-go` via a `replace` directive (same shape as the
# conformance runner Dockerfile).

ARG GO_VERSION=1.25

# ---- Builder stage ----
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /src

COPY livepeer-network-protocol/conformance/runner/go.mod livepeer-network-protocol/conformance/runner/go.sum \
     ./livepeer-network-protocol/conformance/runner/
COPY livepeer-network-protocol/proto-go/ ./livepeer-network-protocol/proto-go/
RUN cd livepeer-network-protocol/conformance/runner && go mod download

COPY livepeer-network-protocol/conformance/runner/ ./livepeer-network-protocol/conformance/runner/

ARG VERSION=dev
RUN cd livepeer-network-protocol/conformance/runner && CGO_ENABLED=0 GOOS=linux \
    go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/session-runner-stub \
        ./session-runner-stub

# ---- Runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/session-runner-stub /bin/session-runner-stub

USER nonroot:nonroot
ENTRYPOINT ["/bin/session-runner-stub"]
