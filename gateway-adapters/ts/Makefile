# gateway-adapters build + test.
# Per core belief #15: every gesture is a docker invocation; no host
# toolchain (no npm, no node) required.

IMAGE   ?= tztcloud/livepeer-gateway-middleware-test
TAG     ?= dev
VERSION := $(shell cat ../../livepeer-network-protocol/VERSION 2>/dev/null || echo dev)

.PHONY: build test shell clean help

build: ## Build the test image (compiles + bundles tests; no test execution)
	docker build \
		--build-arg VERSION=$(VERSION) \
		--target test \
		-t $(IMAGE):$(TAG) \
		-f Dockerfile \
		.

test: build ## Run unit tests in Docker
	docker run --rm $(IMAGE):$(TAG)

shell: build ## Open a shell in the test image (uses node:alpine entrypoint)
	docker run --rm -it --entrypoint sh $(IMAGE):$(TAG)

clean: ## Remove dev image
	docker rmi $(IMAGE):$(TAG) 2>/dev/null || true

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
