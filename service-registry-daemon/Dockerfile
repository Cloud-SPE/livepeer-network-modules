# syntax=docker/dockerfile:1.7

# Build context is the monorepo root (../), not this directory. The daemon's
# go.mod has a `replace github.com/.../chain-commons => ../chain-commons`,
# so the sibling chain-commons tree must be visible to the builder. The
# Makefile's `docker-build` target sets the context for you; if you're
# invoking `docker build` directly, run it from the monorepo root with
# `-f service-registry-daemon/Dockerfile`.

# --- builder ---
FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY chain-commons/ /chain-commons/
COPY proto-contracts/ /proto-contracts/
COPY service-registry-daemon/go.mod service-registry-daemon/go.sum ./
RUN go mod download

COPY service-registry-daemon/ .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -tags 'netgo osusergo' \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/livepeer-service-registry-daemon \
        ./cmd/livepeer-service-registry-daemon

RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -tags 'netgo osusergo' \
        -ldflags "-s -w" \
        -o /out/livepeer-registry-refresh \
        ./cmd/livepeer-registry-refresh

RUN mkdir -p /runtime/var/run/livepeer /runtime/var/lib/livepeer && \
    chown -R 65532:65532 /runtime

# --- runtime ---
FROM gcr.io/distroless/static:nonroot

COPY --from=builder /out/livepeer-service-registry-daemon /usr/local/bin/livepeer-service-registry-daemon
COPY --from=builder /out/livepeer-registry-refresh /usr/local/bin/livepeer-registry-refresh
COPY --from=builder --chown=65532:65532 /runtime/ /

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/livepeer-service-registry-daemon"]
