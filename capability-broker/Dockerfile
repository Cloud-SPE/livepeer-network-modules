# syntax=docker/dockerfile:1
#
# capability-broker image.
# Image: tztcloud/livepeer-capability-broker:<tag>
#
# Build context is the monorepo root because the broker's go.mod has a
# `replace` directive pointing at `../livepeer-network-protocol/proto-go`.
# Both directories must be visible to the build.
#
# Per core belief #16, GO_VERSION ARG is bumped to latest stable on each
# release. Renovate / dependabot tracks this.

ARG GO_VERSION=1.25

# ---- Builder stage ----
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /src

COPY capability-broker/go.mod capability-broker/go.sum ./capability-broker/
COPY livepeer-network-protocol/proto-go/ ./livepeer-network-protocol/proto-go/
RUN cd capability-broker && go mod download

COPY capability-broker/ ./capability-broker/

ARG VERSION=dev
RUN cd capability-broker && CGO_ENABLED=0 GOOS=linux \
    go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/livepeer-capability-broker \
        ./cmd/livepeer-capability-broker

# ---- Runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/livepeer-capability-broker /usr/local/bin/livepeer-capability-broker

USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/livepeer-capability-broker"]
CMD ["--help"]
