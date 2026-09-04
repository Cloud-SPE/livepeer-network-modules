# pool-payout-executor build + test gestures.
# Per core belief #15: every gesture is a docker invocation; no host toolchain
# required at usage time.

IMAGE   ?= tztcloud/livepeer-pool-payout-executor
TAG     ?= dev
VERSION := $(shell cat ../livepeer-network-protocol/VERSION 2>/dev/null || echo dev)

.PHONY: build test run dev publish clean help coverage coverage-check

build: ## Build dev image locally
	docker build \
		--build-arg VERSION=$(VERSION) \
		-t $(IMAGE):$(TAG) \
		-f Dockerfile \
		..

test: ## Run go test ./... inside the test stage
	docker build \
		--target test \
		-t $(IMAGE):$(TAG)-test \
		-f Dockerfile \
		..

run: build ## Preview one payout batch using the example config
	docker run --rm \
		-v $(PWD)/examples/pool-payout-executor-config.example.yaml:/etc/livepeer/pool-payout-executor.yaml:ro \
		-v $(PWD)/examples/keystore.json:/etc/livepeer/keystore.json:ro \
		-v $(PWD)/examples/keystore-password:/etc/livepeer/keystore-password:ro \
		-v $(PWD)/run/data:/var/lib/livepeer/pool-payout-executor \
		$(IMAGE):$(TAG) \
		reconcile-once \
		--config /etc/livepeer/pool-payout-executor.yaml \
		--dry-run

dev: build run ## Build + run in one shot

publish: ## Build + push multi-arch image (requires TAG set to a real version)
	@if [ "$(TAG)" = "dev" ]; then \
		echo "set TAG to a real version before publishing (e.g. make publish TAG=0.1.0)"; \
		exit 1; \
	fi
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--build-arg VERSION=$(TAG) \
		--push \
		-t $(IMAGE):$(TAG) \
		-f Dockerfile \
		..

coverage: ## Write coverage.out for every package that has tests (host Go toolchain; CI runs the same)
	@# Skip the coverage-exempt CLI packages and limit the package list to
	@# packages that actually have tests. No-test packages add no signal to
	@# the gate and can trip missing `covdata` lookups in the selected Go
	@# toolchain. Same convention as protocol-daemon and the registry daemon.
	go test -coverprofile=coverage.out -covermode=atomic \
		$$(go list -f '{{if or (gt (len .TestGoFiles) 0) (gt (len .XTestGoFiles) 0)}}{{.ImportPath}}{{end}}' ./... | sed '/^$$/d' | grep -vE '/cmd/livepeer-pool-payout-executor$$')

coverage-check: coverage ## Fail if any non-exempt package is under the 75% per-package floor (plan 0048 §2.6)
	go run ./lint/coverage-gate --root .

clean: ## Remove dev images
	docker rmi $(IMAGE):$(TAG) 2>/dev/null || true
	docker rmi $(IMAGE):$(TAG)-test 2>/dev/null || true

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
