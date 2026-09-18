.PHONY: conformance build vet

build:
	go build ./...

vet: build
	go vet ./...

conformance: vet
	go run ./cmd/livepeer-conformance
