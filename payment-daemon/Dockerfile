# syntax=docker/dockerfile:1
#
# payment-daemon image.
# Image: tztcloud/livepeer-payment-daemon:<tag>
#
# Per core belief #16, GO_VERSION ARG is bumped to latest stable on each
# release.

ARG GO_VERSION=1.25

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
        -o /out/livepeer-payment-daemon \
        ./cmd/livepeer-payment-daemon

# ---- Test stage (cached separately for `make test`) ----
FROM golang:${GO_VERSION}-alpine AS test

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . ./
RUN go test ./...

# ---- Runtime stage ----
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/livepeer-payment-daemon /usr/local/bin/livepeer-payment-daemon

USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/livepeer-payment-daemon"]
CMD ["--help"]
