ARG GO_VERSION=1.25.7

FROM golang:${GO_VERSION} AS base
WORKDIR /src

COPY proto-contracts/go.mod ./proto-contracts/
COPY livepeer-network-protocol/proto-go/go.mod ./livepeer-network-protocol/proto-go/
COPY pool-reconciler/go.mod pool-reconciler/go.sum ./pool-reconciler/
WORKDIR /src/pool-reconciler
RUN go mod download

COPY proto-contracts/ /src/proto-contracts/
COPY livepeer-network-protocol/proto-go/ /src/livepeer-network-protocol/proto-go/
COPY pool-reconciler/ ./

FROM base AS test
RUN go test ./...

FROM base AS build
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	go build -ldflags="-s -w -X main.version=${VERSION}" \
	-o /out/livepeer-pool-reconciler ./cmd/livepeer-pool-reconciler

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/livepeer-pool-reconciler /usr/local/bin/livepeer-pool-reconciler
ENTRYPOINT ["/usr/local/bin/livepeer-pool-reconciler"]
