# syntax=docker/dockerfile:1
#
# capability-broker image.
# Image: tztcloud/livepeer-capability-broker:<tag>
#
# Build context is the monorepo root because the broker's go.mod has
# `replace` directives pointing at `../livepeer-network-protocol/proto-go`
# and `../livepeer-network-protocol/version`. Every replaced directory
# must be COPYed below, or `go mod download` fails on the one that is
# missing — which is how this build broke when `version` was added.
#
# Per core belief #16, GO_VERSION ARG is bumped to latest stable on each
# release. Renovate / dependabot tracks this.

ARG GO_VERSION=1.25

# ---- Builder stage ----
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /src

COPY capability-broker/go.mod capability-broker/go.sum ./capability-broker/
COPY livepeer-network-protocol/proto-go/ ./livepeer-network-protocol/proto-go/
COPY livepeer-network-protocol/version/ ./livepeer-network-protocol/version/
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
FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ffmpeg ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# /var/lib/livepeer is where session_store.path lives. Mount a persistent
# volume over it in production: it holds paid-session authority and the
# debit sequence counters, so losing it orphans active sessions.
RUN useradd --system --uid 65532 --create-home --home-dir /home/nonroot --shell /usr/sbin/nologin nonroot \
    && mkdir -p /var/lib/livepeer \
    && chown -R 65532:65532 /var/lib/livepeer /home/nonroot

COPY --from=builder /out/livepeer-capability-broker /usr/local/bin/livepeer-capability-broker

USER 65532:65532
# 8080 paid (--listen), 9090 metrics (--metrics), 8443/udp optional
# connected-worker QUIC (listen.worker_quic). The v0 RTMP ingress port
# (1935) went away with the interaction-mode media plane.
EXPOSE 8080 9090 8443/udp
ENTRYPOINT ["/usr/local/bin/livepeer-capability-broker"]
CMD ["--help"]
