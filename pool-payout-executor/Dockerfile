ARG GO_VERSION=1.25.7

FROM golang:${GO_VERSION} AS base
WORKDIR /src

COPY pool-payout-executor/go.mod pool-payout-executor/go.sum ./pool-payout-executor/
WORKDIR /src/pool-payout-executor
RUN go mod download

COPY pool-payout-executor/ ./

FROM base AS test
RUN go test ./...

FROM base AS build
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	go build -ldflags="-s -w -X main.version=${VERSION}" \
	-o /out/livepeer-pool-payout-executor ./cmd/livepeer-pool-payout-executor

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/livepeer-pool-payout-executor /usr/local/bin/livepeer-pool-payout-executor
ENTRYPOINT ["/usr/local/bin/livepeer-pool-payout-executor"]
