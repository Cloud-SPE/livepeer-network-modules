# syntax=docker/dockerfile:1
#
# Protocol conformance suite.
# Image: tztcloud/livepeer-conformance:<tag>
#
# Runs the executable conformance scenarios for paid-job/v1 and
# paid-session/v1 against a broker implementation.
#
# IMPORTANT — this image runs URL MODE only. Auto mode builds and drives
# the in-repo reference broker from source, which a published image does
# not carry; use `make conformance` from a checkout for that.
#
# The suite hosts its own fake job backend and fake session runner, and
# the broker under test must be able to reach them. They bind loopback,
# so run with host networking:
#
#   docker run --rm --network host tztcloud/livepeer-conformance:<tag> \
#       --broker-url http://127.0.0.1:8080 --pause
#
# Without --network host the broker cannot reach the fakes and every
# session scenario fails at create. See README.md for the offerings the
# broker under test must serve.

ARG GO_VERSION=1.25

# ---- Builder stage ----
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /src

COPY livepeer-network-protocol/conformance/go.mod livepeer-network-protocol/conformance/go.sum ./
RUN go mod download

COPY livepeer-network-protocol/conformance/ ./

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux \
    go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/livepeer-conformance \
        ./cmd/livepeer-conformance

# ---- Runtime stage ----
FROM debian:bookworm-slim

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN useradd --system --uid 65532 --create-home --home-dir /home/nonroot --shell /usr/sbin/nologin nonroot

COPY --from=builder /out/livepeer-conformance /usr/local/bin/livepeer-conformance

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/livepeer-conformance"]
CMD ["--help"]
