# syntax=docker/dockerfile:1
#
# payment-daemon image.
# Image: tztcloud/livepeer-payment-daemon:<tag>
#
# Build context is the monorepo root because the daemon's go.mod has a
# `replace` directive pointing at `../livepeer-network-protocol/proto-go`.
# Both directories must be visible to the build.
#
# Per core belief #16, GO_VERSION ARG is bumped to latest stable on each
# release.

ARG GO_VERSION=1.25

# ---- Builder stage ----
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /src

COPY payment-daemon/go.mod payment-daemon/go.sum ./payment-daemon/
COPY livepeer-network-protocol/proto-go/ ./livepeer-network-protocol/proto-go/
RUN cd payment-daemon && go mod download

COPY payment-daemon/ ./payment-daemon/

ARG VERSION=dev
RUN cd payment-daemon && CGO_ENABLED=0 GOOS=linux \
    go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/livepeer-payment-daemon \
        ./cmd/livepeer-payment-daemon

# Pre-create the runtime data + socket directories so Docker named volumes
# mounted at these paths inherit the nonroot ownership rather than the
# default root-owned-empty-dir behavior.
RUN install -d -m 0700 -o 65532 -g 65532 /out/data \
    && install -d -m 0700 -o 65532 -g 65532 /out/run

# ---- Test stage (cached separately for `make test`) ----
FROM golang:${GO_VERSION}-alpine AS test

WORKDIR /src
COPY payment-daemon/go.mod payment-daemon/go.sum ./payment-daemon/
COPY livepeer-network-protocol/proto-go/ ./livepeer-network-protocol/proto-go/
RUN cd payment-daemon && go mod download
COPY payment-daemon/ ./payment-daemon/
RUN cd payment-daemon && go test ./...

# ---- Runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/livepeer-payment-daemon /usr/local/bin/livepeer-payment-daemon

# nonroot UID is 65532 in distroless; --chown carries through. These dirs
# are the canonical mount points for the BoltDB ledger and the unix socket.
COPY --from=builder --chown=nonroot:nonroot /out/data /var/lib/livepeer/payment-daemon
COPY --from=builder --chown=nonroot:nonroot /out/run  /var/run/livepeer

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/livepeer-payment-daemon"]
CMD ["--help"]
