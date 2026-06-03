FROM golang:1.25.7-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
RUN go build -o /out/pool-member-agent ./cmd/pool-member-agent

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/pool-member-agent /usr/local/bin/pool-member-agent
ENTRYPOINT ["/usr/local/bin/pool-member-agent"]
