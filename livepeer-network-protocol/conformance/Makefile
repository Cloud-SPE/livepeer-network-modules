# Conformance runner build + test.
# Per core belief #15: every gesture is a docker invocation.

IMAGE   ?= tztcloud/livepeer-conformance
TAG     ?= dev
VERSION := $(shell cat ../VERSION 2>/dev/null || echo dev)

.PHONY: build test test-compose publish clean help

build: ## Build dev image locally
	docker build \
		--build-arg VERSION=$(VERSION) \
		-t $(IMAGE):$(TAG) \
		-f Dockerfile \
		.

test: ## Run conformance against $(BROKER_URL); set MODES to filter (comma list)
	@if [ -z "$(BROKER_URL)" ]; then \
		echo "BROKER_URL is required"; \
		echo "  example: make test BROKER_URL=http://host.docker.internal:8080"; \
		exit 1; \
	fi
	docker run --rm \
		--add-host=host.docker.internal:host-gateway \
		$(IMAGE):$(TAG) \
		--target broker \
		--url $(BROKER_URL) \
		$(if $(MODES),--modes $(MODES),)

test-compose: build ## Run the full conformance stack (broker + runner) and propagate exit code
	@docker image inspect tztcloud/livepeer-capability-broker:dev >/dev/null 2>&1 || \
		(echo "broker image missing. Build it first: make -C ../../capability-broker build"; exit 1)
	docker compose -f compose.yaml up --abort-on-container-exit --exit-code-from runner

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
		.

clean: ## Remove dev images
	docker rmi $(IMAGE):$(TAG) 2>/dev/null || true

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
