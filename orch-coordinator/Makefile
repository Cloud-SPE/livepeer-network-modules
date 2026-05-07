# orch-coordinator build + test gestures.
# Per core belief #15: every gesture is a docker invocation; no host
# toolchain required at usage time.

IMAGE   ?= tztcloud/livepeer-orch-coordinator
TAG     ?= dev
VERSION := $(shell cat ../livepeer-network-protocol/VERSION 2>/dev/null || echo dev)

.PHONY: build run dev test smoke publish clean help

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

run: ## Foreground the binary in --dev mode (LAN listener on 127.0.0.1:8080 by default)
	mkdir -p run/data
	docker run --rm \
		-p 127.0.0.1:8080:8080 \
		-p 127.0.0.1:8081:8081 \
		-p 127.0.0.1:9091:9091 \
		-v $(PWD)/run/data:/srv/data \
		-v $(PWD)/examples/coordinator-config.yaml:/etc/livepeer/orch-coordinator.yaml:ro \
		$(IMAGE):$(TAG) \
			--config=/etc/livepeer/orch-coordinator.yaml \
			--data-dir=/srv/data \
			--listen=0.0.0.0:8080 \
			--public-listen=0.0.0.0:8081 \
			--metrics-listen=0.0.0.0:9091 \
			--dev

dev: build run ## Build + run in one shot

smoke: ## In-container go test (-race, full surface)
	docker build \
		--target test \
		-t $(IMAGE):$(TAG)-smoke \
		-f Dockerfile \
		..

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
