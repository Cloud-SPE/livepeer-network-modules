.PHONY: build test lint lint-custom doc-lint coverage coverage-check docker-build docker-build-version docker-push clean tidy ship-check

DOCKER_REPO ?= tztcloud/livepeer-service-registry-daemon
DOCKER_TAG  ?= dev
VERSION ?= $(shell VERSION_PREFIX="$(DOCKER_TAG)" FALLBACK_VERSION=dev ../infra/build/git-version.sh)
GO_LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	go build -trimpath -ldflags '$(GO_LDFLAGS)' -o bin/livepeer-service-registry-daemon ./cmd/livepeer-service-registry-daemon
	go build -o bin/livepeer-registry-refresh ./cmd/livepeer-registry-refresh

test:
	go test -race ./...

lint:
	golangci-lint run ./...
	$(MAKE) lint-custom

lint-custom:
	go run ./lint/doc-gardener --root .
	go run ./lint/no-unverified-manifest --root .

doc-lint:
	go run ./lint/doc-gardener --root .

coverage:
	@# Limit the coverage run to packages that actually have tests. This
	@# skips generated/example/doc-only packages, which add no signal to
	@# the local gate and can trip missing `covdata` lookups.
	go test -coverprofile=coverage.out -covermode=atomic \
		$$(go list -f '{{if or (gt (len .TestGoFiles) 0) (gt (len .XTestGoFiles) 0)}}{{.ImportPath}}{{end}}' ./... | sed '/^$$/d')

coverage-check: coverage
	go run ./lint/coverage-gate --root .

# ship-check is the pre-tag gate. Run before cutting a release tag.
# Catches everything CI catches: golangci-lint, race-detector tests,
# per-package coverage floor, and proto wire-compat breaking-change check.
# See protocol-daemon/Makefile for why coverage-check + proto-check
# are not part of the local ship-check gate (env tooling flakes).
ship-check:
	$(MAKE) lint
	$(MAKE) test

tidy:
	go mod tidy

docker-build:
	docker build --build-arg VERSION=$(VERSION) -t $(DOCKER_REPO):$(DOCKER_TAG) -f Dockerfile ..

# Stamp the build with the git short SHA — handy for CI runs where
# `dev` would collide across builds.
docker-build-version:
	@DOCKER_TAG=$$(git rev-parse --short HEAD); \
	VERSION=$$(FALLBACK_VERSION=dev ../infra/build/git-version.sh); \
	docker build --build-arg VERSION="$$VERSION" -t $(DOCKER_REPO):$$DOCKER_TAG -f Dockerfile ..

# Push DOCKER_TAG plus :latest. Override DOCKER_TAG to publish a
# version: `make docker-push DOCKER_TAG=v1.4.0`.
docker-push:
	docker push $(DOCKER_REPO):$(DOCKER_TAG)
	docker tag $(DOCKER_REPO):$(DOCKER_TAG) $(DOCKER_REPO):latest
	docker push $(DOCKER_REPO):latest

clean:
	rm -rf bin/
