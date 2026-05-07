FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /bin/chat-runner ./cmd/chat \
 && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o /bin/embeddings-runner ./cmd/embeddings

FROM alpine:3.20 AS chat
RUN apk add --no-cache curl ca-certificates && adduser -D -H runner
USER runner
ENV RUNNER_ADDR=:8080
ENV CAPABILITY_NAME=openai-chat-completions
EXPOSE 8080
COPY --from=build /bin/chat-runner /runner
COPY offering.yaml /etc/runner/offering.yaml
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD curl -fsS http://localhost:8080/healthz || exit 1
ENTRYPOINT ["/runner"]

FROM alpine:3.20 AS embeddings
RUN apk add --no-cache curl ca-certificates && adduser -D -H runner
USER runner
ENV RUNNER_ADDR=:8080
ENV CAPABILITY_NAME=openai-text-embeddings
EXPOSE 8080
COPY --from=build /bin/embeddings-runner /runner
COPY offering.yaml /etc/runner/offering.yaml
HEALTHCHECK --interval=30s --timeout=5s --retries=3 \
    CMD curl -fsS http://localhost:8080/healthz || exit 1
ENTRYPOINT ["/runner"]
