# syntax=docker/dockerfile:1
#
# secure-orch-console image.
# Image: tztcloud/livepeer-secure-orch-console:<tag>
#
# Build context is the monorepo root. The runtime stage stays
# distroless-static; the binary binds 127.0.0.1 only — confirmed by a
# unit test on the loopback gate (cmd/secure-orch-console/main_test.go).
#
# Per core belief #16, the GO_VERSION ARG is bumped to latest stable
# on each release.

ARG GO_VERSION=1.25

# ---- Builder stage ----
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /src

COPY secure-orch-console/go.mod secure-orch-console/go.sum ./secure-orch-console/
RUN cd secure-orch-console && go mod download

COPY secure-orch-console/ ./secure-orch-console/

ARG VERSION=dev
RUN cd secure-orch-console && CGO_ENABLED=0 GOOS=linux \
    go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/secure-orch-console \
        ./cmd/secure-orch-console \
    && CGO_ENABLED=0 GOOS=linux \
    go build \
        -trimpath \
        -ldflags="-s -w" \
        -o /out/secure-orch-keygen \
        ./cmd/secure-orch-keygen

RUN install -d -m 0700 -o 65532 -g 65532 /out/srv

# ---- Test stage ----
FROM golang:${GO_VERSION}-alpine AS test

WORKDIR /src
COPY secure-orch-console/go.mod secure-orch-console/go.sum ./secure-orch-console/
RUN cd secure-orch-console && go mod download
COPY secure-orch-console/ ./secure-orch-console/
RUN cd secure-orch-console && go test -race ./...

# ---- Runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/secure-orch-console /usr/local/bin/secure-orch-console
COPY --from=builder /out/secure-orch-keygen  /usr/local/bin/secure-orch-keygen

# Default mount point for the operator's spool / state / log dirs.
COPY --from=builder --chown=nonroot:nonroot /out/srv /srv

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/secure-orch-console"]
CMD ["--help"]
