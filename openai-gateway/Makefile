IMAGE   ?= tztcloud/livepeer-openai-gateway-reference
TAG     ?= dev
VERSION := $(shell cat ../livepeer-network-protocol/VERSION 2>/dev/null || echo dev)

.PHONY: build run smoke clean help

build: ## Build the gateway image
	docker build \
		--build-arg VERSION=$(VERSION) \
		-t $(IMAGE):$(TAG) \
		-f Dockerfile \
		.

run: build ## Run the gateway alone (for hand-curl testing). Set LIVEPEER_BROKER_URL.
	@if [ -z "$(LIVEPEER_BROKER_URL)" ]; then \
		echo "LIVEPEER_BROKER_URL is required (e.g. make run LIVEPEER_BROKER_URL=http://host.docker.internal:8080)"; exit 1; \
	fi
	docker run --rm \
		-p 3000:3000 \
		-e LIVEPEER_BROKER_URL=$(LIVEPEER_BROKER_URL) \
		--add-host=host.docker.internal:host-gateway \
		$(IMAGE):$(TAG)

smoke: build ## End-to-end Docker smoke test (gateway + broker + mock-backend)
	@docker image inspect tztcloud/livepeer-capability-broker:dev >/dev/null 2>&1 || \
		(echo "broker image missing. Build it first: make -C ../capability-broker build"; exit 1)
	./scripts/smoke.sh

clean: ## Remove dev images
	docker rmi $(IMAGE):$(TAG) 2>/dev/null || true

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
