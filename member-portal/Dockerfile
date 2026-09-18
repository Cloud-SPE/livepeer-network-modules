FROM golang:1.26 AS base
WORKDIR /src
COPY pool-commons/ ./pool-commons/
COPY member-portal/ ./member-portal/
WORKDIR /src/member-portal
RUN go mod download
FROM base AS test
RUN go test -race ./...
FROM base AS build
RUN CGO_ENABLED=0 go build -trimpath -o /out/livepeer-member-portal ./cmd/livepeer-member-portal
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/livepeer-member-portal /usr/local/bin/livepeer-member-portal
ENTRYPOINT ["/usr/local/bin/livepeer-member-portal"]
