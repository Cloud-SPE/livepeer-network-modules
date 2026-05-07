# syntax=docker/dockerfile:1
#
# orch-coordinator image.
# Image: tztcloud/livepeer-orch-coordinator:<tag>
#
# Build context is the monorepo root because the coordinator imports
# `../livepeer-network-protocol/verify/` for shared signature recovery.
# Both directories must be visible to the build.
#
# Per core belief #16, GO_VERSION ARG is bumped to latest stable on each
# release.

ARG GO_VERSION=1.25

# ---- Builder stage ----
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /src

COPY orch-coordinator/go.mod orch-coordinator/go.sum ./orch-coordinator/
COPY livepeer-network-protocol/ ./livepeer-network-protocol/
RUN cd orch-coordinator && go mod download

COPY orch-coordinator/ ./orch-coordinator/

ARG VERSION=dev
RUN cd orch-coordinator && CGO_ENABLED=0 GOOS=linux \
    go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/livepeer-orch-coordinator \
        ./cmd/livepeer-orch-coordinator

RUN install -d -m 0700 -o 65532 -g 65532 /out/srv/data

# ---- Test stage ----
FROM golang:${GO_VERSION}-alpine AS test

WORKDIR /src
COPY orch-coordinator/go.mod orch-coordinator/go.sum ./orch-coordinator/
COPY livepeer-network-protocol/ ./livepeer-network-protocol/
RUN cd orch-coordinator && go mod download
COPY orch-coordinator/ ./orch-coordinator/
RUN cd orch-coordinator && go test -race -count=1 ./...

# ---- Runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/livepeer-orch-coordinator /usr/local/bin/livepeer-orch-coordinator
COPY --from=builder --chown=nonroot:nonroot /out/srv /srv

USER nonroot:nonroot
EXPOSE 8080 8081 9091
ENTRYPOINT ["/usr/local/bin/livepeer-orch-coordinator"]
CMD ["--help"]
