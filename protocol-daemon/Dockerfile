# syntax=docker/dockerfile:1.7

# --- builder ---
# CGO_ENABLED=0 produces a statically-linked binary that drops straight
# into distroless/static. go-ethereum's crypto has a pure-Go fallback;
# `netgo,osusergo` guarantee no glibc resolver / user-lookup shims.
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Prime the module cache. chain-commons sits a directory up; copy it in
# first so the replace directive resolves.
COPY chain-commons/ /chain-commons/
COPY proto-contracts/ /proto-contracts/
COPY protocol-daemon/go.mod protocol-daemon/go.sum ./
RUN go mod download

COPY protocol-daemon/ .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -tags 'netgo osusergo' \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/livepeer-protocol-daemon \
        ./cmd/livepeer-protocol-daemon

# Pre-create runtime paths owned by the nonroot uid (65532). Docker
# volumes mounted on top of these paths inherit the image ownership.
RUN mkdir -p /runtime/var/run/livepeer /runtime/var/lib/livepeer && \
    chown -R 65532:65532 /runtime

# --- runtime ---
FROM gcr.io/distroless/static:nonroot

COPY --from=builder /out/livepeer-protocol-daemon /usr/local/bin/livepeer-protocol-daemon
COPY --from=builder --chown=65532:65532 /runtime/ /

USER nonroot:nonroot

ENTRYPOINT ["/usr/local/bin/livepeer-protocol-daemon"]
