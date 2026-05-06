# syntax=docker/dockerfile:1
#
# Conformance runner image. Distroless final stage; fixtures baked in.
# Image: tztcloud/livepeer-conformance:<spec-VERSION>
#
# Per core belief #16, the GO_VERSION ARG is bumped to latest stable on each
# release. Renovate / dependabot tracks this.

ARG GO_VERSION=1.24

# ---- Builder stage ----
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /src

# Copy module files first for layer caching.
COPY runner/go.mod runner/go.sum ./
RUN go mod download

# Copy source and build.
COPY runner/ ./

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux \
    go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/livepeer-conformance \
        ./cmd/livepeer-conformance

# ---- Runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/livepeer-conformance /usr/local/bin/livepeer-conformance
COPY fixtures/ /fixtures/

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/livepeer-conformance"]
CMD ["--help"]
