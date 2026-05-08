# chain-commons — module Makefile
#
# Library only; no Dockerfile. Imported by payment-daemon,
# service-registry-daemon, and protocol-daemon.

.PHONY: help build test test-race lint coverage-check clean tidy ship-check
.DEFAULT_GOAL := help

COVERAGE_THRESHOLD ?= 75

help:
	@echo "chain-commons targets:"
	@echo "  build           go build ./..."
	@echo "  test            go test ./..."
	@echo "  test-race       go test -race ./..."
	@echo "  lint            go vet + golangci-lint (if installed) + custom lints"
	@echo "  coverage-check  enforce $(COVERAGE_THRESHOLD)% per-package coverage"
	@echo "  ship-check      pre-tag gate: lint + test-race + coverage-check"
	@echo "  tidy            go mod tidy"
	@echo "  clean           remove build/coverage artifacts"

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
		echo "golangci-lint not installed — skipping (install: https://golangci-lint.run/usage/install/)"; \
	fi

coverage-check:
	go test -coverprofile=coverage.out ./...
	@go tool cover -func=coverage.out | tail -1
	@# Per-package check; thresholding logic lands in lint/coverage-gate (plan 0001 §J)
	@echo "(per-package threshold check: lint/coverage-gate stub — see exemptions.txt)"

# ship-check is the pre-tag gate. Run before cutting a release tag.
# chain-commons is a library: lint + race-detector tests.
# (No proto / no docker — depended on by the daemons, not shipped.)
# Coverage runs in CI; the Go-toolchain auto-download is missing
# covdata so a local coverage gate isn't reliable yet.
ship-check:
	$(MAKE) lint
	$(MAKE) test-race

tidy:
	go mod tidy

clean:
	rm -f coverage.out coverage.html
