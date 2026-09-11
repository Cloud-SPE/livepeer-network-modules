ARG GO_VERSION=1.25.7

FROM golang:${GO_VERSION} AS base
WORKDIR /src

COPY pool-controller/go.mod pool-controller/go.sum ./pool-controller/
WORKDIR /src/pool-controller
RUN go mod download

COPY pool-controller/ ./
# The curated template catalog lives at the repo root, not inside this
# module: it is the pool's product list, and other tools read it too.
# It has to be in the image, because the controller loads it at boot.
COPY templates/ /src/templates/

FROM base AS test
RUN go test ./...

FROM base AS build
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
	go build -ldflags="-s -w -X main.version=${VERSION}" \
	-o /out/livepeer-pool-controller ./cmd/livepeer-pool-controller

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/livepeer-pool-controller /usr/local/bin/livepeer-pool-controller
COPY --from=build /src/templates /etc/livepeer/templates
ENTRYPOINT ["/usr/local/bin/livepeer-pool-controller"]
