ARG GO_VERSION=1.25.7

FROM golang:${GO_VERSION} AS base
WORKDIR /src

COPY pool-controller/go.mod pool-controller/go.sum ./pool-controller/
WORKDIR /src/pool-controller
RUN go mod download

COPY pool-controller/ ./

FROM base AS test
RUN go test ./...

FROM base AS build
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	go build -ldflags="-s -w -X main.version=${VERSION}" \
	-o /out/livepeer-pool-controller ./cmd/livepeer-pool-controller
# broker-apply: the bootstrap.broker_apply_command staging helper, shipped so the
# distroless image can stage rendered broker config without a shell/coreutils.
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	go build -ldflags="-s -w" \
	-o /out/broker-apply ./cmd/broker-apply

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/livepeer-pool-controller /usr/local/bin/livepeer-pool-controller
COPY --from=build /out/broker-apply /usr/local/bin/broker-apply
ENTRYPOINT ["/usr/local/bin/livepeer-pool-controller"]
