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
# the broker under test must be able to reach them. They bind loopback by
# default, so on a shared docker network bind and advertise them:
#
#   docker run --rm --network lpm_default --name conformance \
#       tztcloud/livepeer-conformance:<tag> \
#       --broker-url http://broker:8080 \
#       --fakes-listen 0.0.0.0 --fakes-advertise conformance \
#       --fakes-backend-port 8091 --fakes-runner-port 8092 --warmup 20s
#
# Point the broker's offerings at http://conformance:8091 (job backend)
# and http://conformance:8092 (session runner); --warmup lets a broker
# that started first recover the health probes it failed against them.
# examples/docker-network/ has a working compose file. With --network host the
# defaults still work:
#
#   docker run --rm --network host tztcloud/livepeer-conformance:<tag> \
#       --broker-url http://127.0.0.1:8080 --pause
#
# See README.md for the offerings the broker under test must serve.

ARG GO_VERSION=1.25

# ---- Builder stage ----
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /src

# The module's replace directives point at ../verify and ../proto-go, so the
# builder has to mirror the repo layout rather than flatten conformance into
# /src — otherwise `go mod download` resolves them to /verify and /proto-go
# and finds nothing there.
COPY livepeer-network-protocol/conformance/go.mod livepeer-network-protocol/conformance/go.sum ./livepeer-network-protocol/conformance/
COPY livepeer-network-protocol/verify/ ./livepeer-network-protocol/verify/
COPY livepeer-network-protocol/proto-go/ ./livepeer-network-protocol/proto-go/
RUN cd livepeer-network-protocol/conformance && go mod download

COPY livepeer-network-protocol/conformance/ ./livepeer-network-protocol/conformance/

ARG VERSION=dev
RUN cd livepeer-network-protocol/conformance && CGO_ENABLED=0 GOOS=linux \
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
