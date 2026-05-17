# pool-controller build + test gestures.
# Per core belief #15: every gesture is a docker invocation; no host toolchain
# required at usage time.

IMAGE   ?= tztcloud/livepeer-pool-controller
TAG     ?= dev
VERSION := $(shell cat ../livepeer-network-protocol/VERSION 2>/dev/null || echo dev)

.PHONY: build test run dev publish clean help

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

run: build ## Run the minimal admin server with the example input
	docker run --rm \
		-p 8080:8080 \
		-v $(PWD)/examples/pool-controller-config.example.yaml:/etc/livepeer/pool-controller.yaml:ro \
		-v $(PWD)/run/data:/var/lib/livepeer/pool-controller \
		$(IMAGE):$(TAG) \
		serve \
		--config /etc/livepeer/pool-controller.yaml \
		--data-dir /var/lib/livepeer/pool-controller \
		--listen :8080

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

clean: ## Remove dev images
	docker rmi $(IMAGE):$(TAG) 2>/dev/null || true
	docker rmi $(IMAGE):$(TAG)-test 2>/dev/null || true

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
