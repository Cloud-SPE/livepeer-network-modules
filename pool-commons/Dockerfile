ARG GO_VERSION=1.25.7
FROM golang:${GO_VERSION} AS test
WORKDIR /src
COPY pool-commons/ ./
RUN go test -race ./...
FROM test AS build
RUN go build ./...
