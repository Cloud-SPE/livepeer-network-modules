# capability-broker build + run.
# Per core belief #15: every gesture is a docker invocation; no host toolchain
# required.

IMAGE   ?= tztcloud/livepeer-capability-broker
TAG     ?= v2.0.0
VERSION ?= $(shell VERSION_PREFIX="$(TAG)" FALLBACK_VERSION=v2.0.0 ../infra/build/git-version.sh)

.PHONY: build run dev smoke publish clean help

build: ## Build dev image locally
	docker build \
		--build-arg VERSION=$(VERSION) \
		-t $(IMAGE):$(TAG) \
		-f Dockerfile \
		..

run: ## Run with examples/host-config.example.yaml (needs BROKER_ADMIN_TOKEN, a seal key + a payment daemon — see the file header)
	docker run --rm \
		-p 8080:8080 \
		-p 9090:9090 \
		-v $(PWD)/examples/host-config.example.yaml:/etc/livepeer/host-config.yaml:ro \
		$(IMAGE):$(TAG) \
		--config /etc/livepeer/host-config.yaml

dev: build run ## Build + run in one shot

smoke: ## End-to-end Docker smoke test (mock backend + broker + assertions)
	./scripts/smoke.sh

publish: ## Build + push multi-arch image (requires TAG set to a real version)
	@if [ "$(TAG)" = "dev" ]; then \
		echo "set TAG to a real version before publishing (e.g. make publish TAG=v2.0.0)"; \
		exit 1; \
	fi
	docker buildx build \
		--platform linux/amd64,linux/arm64 \
		--build-arg VERSION=$(VERSION) \
		--push \
		-t $(IMAGE):$(TAG) \
		-f Dockerfile \
		..

clean: ## Remove dev images
	docker rmi $(IMAGE):$(TAG) 2>/dev/null || true

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
