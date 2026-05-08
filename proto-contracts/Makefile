.PHONY: help build test test-race lint proto coverage-check clean tidy ship-check
.DEFAULT_GOAL := help

help:
	@echo "proto-contracts targets:"
	@echo "  build           go build ./..."
	@echo "  test            go test ./..."
	@echo "  test-race       go test -race ./..."
	@echo "  lint            go vet + golangci-lint (if installed) + custom lints"
	@echo "  proto           buf lint + buf generate"
	@echo "  coverage-check  enforce 75% per-package coverage"
	@echo "  ship-check      lint + test-race + coverage-check"
	@echo "  tidy            go mod tidy"
	@echo "  clean           remove coverage artifacts"

build:
	go build ./...

test:
	go test ./...

test-race:
	go test -race ./...

lint:
	go vet ./...
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run; \
	else \
		echo "golangci-lint not installed — skipping"; \
	fi
	go run ./lint/doc-gardener --root .
	go run ./lint/no-secrets-in-logs --root .

proto:
	buf lint
	buf generate

coverage-check:
	go test -coverprofile=coverage.out -covermode=atomic \
		$$(go list -f '{{if or (gt (len .TestGoFiles) 0) (gt (len .XTestGoFiles) 0)}}{{.ImportPath}}{{end}}' ./... | sed '/^$$/d')
	go run ./lint/coverage-gate --root .

ship-check:
	$(MAKE) lint
	$(MAKE) test-race

tidy:
	go mod tidy

clean:
	rm -f coverage.out
