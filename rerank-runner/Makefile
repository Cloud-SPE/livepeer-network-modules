# rerank-runner build + smoke.
# Per core belief #15: every gesture is a docker invocation; no host toolchain
# required.

REGISTRY    ?= tztcloud
TAG         ?= v1.1.0
BASE_IMAGE  ?= $(REGISTRY)/python-runner-base:$(TAG)

.PHONY: build runner downloader smoke clean help

build: runner downloader ## Build runner + downloader images

runner: ## Build the rerank-runner image (FROM python-runner-base)
	docker build \
		--build-arg BASE_IMAGE=$(BASE_IMAGE) \
		-t $(REGISTRY)/rerank-runner:$(TAG) \
		-f Dockerfile \
		.

downloader: ## Build the model-downloader image
	docker build \
		-t $(REGISTRY)/rerank-model-downloader:$(TAG) \
		-f model-downloader/Dockerfile \
		model-downloader

smoke: ## Smoke against fixture query + docs
	./build.sh smoke

clean: ## Remove dev images
	docker rmi \
		$(REGISTRY)/rerank-runner:$(TAG) \
		$(REGISTRY)/rerank-model-downloader:$(TAG) \
		2>/dev/null || true

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
