.PHONY: build test test-race lint lint-custom doc-lint coverage coverage-check docker-build clean ship-check

BIN := bin/livepeer-protocol-daemon
DOCKER_TAG ?= dev

build:
	go build -o $(BIN) ./cmd/livepeer-protocol-daemon

test:
	go test ./...

test-race:
	go test -race ./...

lint:
	golangci-lint run ./...
	$(MAKE) lint-custom

# ship-check is the pre-tag gate. Run before cutting a release tag.
# Catches the lint + test classes that would have caught v2.0.1's CI
# failures locally: golangci-lint v2 (goimports, gosec, revive,
# unconvert) and race-detector tests. Two CI-side gates we
# deliberately leave OUT of local ship-check because their local
# tooling is currently flaky:
#   - coverage-check: auto-downloaded Go toolchain missing covdata
#     (any package without _test.go files trips `go: no such tool`)
#   - proto-check: `buf breaking --against '.git#...'` resolves
#     .git relative to cwd inside proto/ and fails locally; only
#     works in the CI environment where the workflow checkouts the
#     full repo at the right base
# CI still runs both, so they're not skipped — just not duplicated
# by this gate.
ship-check:
	$(MAKE) lint
	$(MAKE) test-race

lint-custom:
	go run ./lint/no-secrets-in-logs --root .
	go run ./lint/doc-gardener --root .
	go run ./lint/layer-check --root .

doc-lint:
	go run ./lint/doc-gardener --root .

coverage:
	@# Skip the coverage-exempt CLI package and limit the package list to
	@# packages that actually have tests. No-test generated/example/doc
	@# packages add no signal to the gate and can trip missing `covdata`
	@# lookups in the selected Go toolchain.
	go test -coverprofile=coverage.out -covermode=atomic \
		$$(go list -f '{{if or (gt (len .TestGoFiles) 0) (gt (len .XTestGoFiles) 0)}}{{.ImportPath}}{{end}}' ./... | sed '/^$$/d' | grep -v '/cmd/livepeer-protocol-daemon$$')

coverage-check: coverage
	go run ./lint/coverage-gate --root .

# Build context is the monorepo root because the daemon's go.mod uses
# `replace ../chain-commons` and the Dockerfile copies the sibling tree in.
docker-build:
	docker build --build-arg VERSION=$(DOCKER_TAG) -t tztcloud/livepeer-protocol-daemon:$(DOCKER_TAG) -f Dockerfile ..

clean:
	rm -rf bin/ coverage.out
