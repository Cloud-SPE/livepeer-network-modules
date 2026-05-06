# syntax=docker/dockerfile:1
#
# secure-orch-console image.
# Image: tztcloud/livepeer-secure-orch-console:<tag>
#
# Build context is the monorepo root. The console module's go.mod is
# self-contained today; the runtime stage stays distroless-static and
# binds 127.0.0.1 only.
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
# The console binary lands in commit 4 (plan 0019 §12). Until then
# the builder stage proves the module compiles.
RUN cd secure-orch-console && go build ./...

# ---- Test stage ----
FROM golang:${GO_VERSION}-alpine AS test

WORKDIR /src
COPY secure-orch-console/go.mod secure-orch-console/go.sum ./secure-orch-console/
RUN cd secure-orch-console && go mod download
COPY secure-orch-console/ ./secure-orch-console/
RUN cd secure-orch-console && go test -race ./...

# ---- Runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot

# The binary entrypoint lands in commit 4. Keep the runtime stage in
# place so the build target succeeds and the image shape is locked
# from day one.
COPY --from=builder /src/secure-orch-console/go.mod /app/

USER nonroot:nonroot
ENTRYPOINT ["/bin/sh"]
