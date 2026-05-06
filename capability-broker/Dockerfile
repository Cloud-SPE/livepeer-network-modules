# syntax=docker/dockerfile:1
#
# capability-broker image.
# Image: tztcloud/livepeer-capability-broker:<tag>
#
# Per core belief #16, GO_VERSION ARG is bumped to latest stable on each
# release. Renovate / dependabot tracks this.

ARG GO_VERSION=1.23

# ---- Builder stage ----
FROM golang:${GO_VERSION}-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . ./

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux \
    go build \
        -trimpath \
        -ldflags="-s -w -X main.version=${VERSION}" \
        -o /out/livepeer-capability-broker \
        ./cmd/livepeer-capability-broker

# ---- Runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/livepeer-capability-broker /usr/local/bin/livepeer-capability-broker

USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/usr/local/bin/livepeer-capability-broker"]
CMD ["--help"]
