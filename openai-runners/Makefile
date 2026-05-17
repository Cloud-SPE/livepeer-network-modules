# openai-runners build + smoke.
# Per core belief #15: every gesture is a docker invocation; no host toolchain
# required.

REGISTRY    ?= tztcloud
TAG         ?= v1.2.0
PLATFORMS   ?= linux/amd64

.PHONY: build base gpu-base gpu-media-base chat-runner embeddings-runner audio tts image downloader tester smoke clean help

build: base gpu-base gpu-media-base chat-runner embeddings-runner audio tts image downloader tester ## Build all runner images locally

base: ## Build the shared python-runner-base image
	docker build \
		-t $(REGISTRY)/python-runner-base:$(TAG) \
		-f python-runner-base/Dockerfile \
		python-runner-base

gpu-base: ## Build the shared python-gpu-runner-base image
	docker build \
		-t $(REGISTRY)/python-gpu-runner-base:$(TAG) \
		-f python-gpu-runner-base/Dockerfile \
		python-gpu-runner-base

gpu-media-base: gpu-base ## Build the shared python-gpu-media-runner-base image
	docker build \
		--build-arg BASE_IMAGE=$(REGISTRY)/python-gpu-runner-base:$(TAG) \
		-t $(REGISTRY)/python-gpu-media-runner-base:$(TAG) \
		-f python-gpu-media-runner-base/Dockerfile \
		python-gpu-media-runner-base

chat-runner: ## Build the streaming-aware openai-chat-runner image (multi-arch)
	docker buildx build \
		--platform $(PLATFORMS) \
		-t $(REGISTRY)/openai-chat-runner:$(TAG) \
		--load \
		-f openai-chat-runner/Dockerfile \
		openai-chat-runner

embeddings-runner: ## Build the openai-embeddings-runner image (multi-arch)
	docker buildx build \
		--platform $(PLATFORMS) \
		-t $(REGISTRY)/openai-embeddings-runner:$(TAG) \
		--load \
		-f openai-embeddings-runner/Dockerfile \
		openai-embeddings-runner

audio: gpu-media-base ## Build the openai-audio-runner image
	docker build \
		--build-arg BASE_IMAGE=$(REGISTRY)/python-gpu-media-runner-base:$(TAG) \
		-t $(REGISTRY)/openai-audio-runner:$(TAG) \
		-f openai-audio-runner/Dockerfile \
		openai-audio-runner

tts: gpu-media-base ## Build the openai-tts-runner image
	docker build \
		--build-arg BASE_IMAGE=$(REGISTRY)/python-gpu-media-runner-base:$(TAG) \
		-t $(REGISTRY)/openai-tts-runner:$(TAG) \
		-f openai-tts-runner/Dockerfile \
		openai-tts-runner

image: gpu-base ## Build the openai-image-generation-runner image
	docker build \
		--build-arg BASE_IMAGE=$(REGISTRY)/python-gpu-runner-base:$(TAG) \
		-t $(REGISTRY)/openai-image-generation-runner:$(TAG) \
		-f openai-image-generation-runner/Dockerfile \
		openai-image-generation-runner

downloader: ## Build the image-model-downloader image
	docker build \
		-t $(REGISTRY)/image-model-downloader:$(TAG) \
		-f image-model-downloader/Dockerfile \
		image-model-downloader

tester: ## Build the openai-tester image (Node integration harness)
	docker build \
		-t $(REGISTRY)/openai-tester:$(TAG) \
		-f openai-tester/Dockerfile \
		openai-tester

smoke: ## Cross-runner smoke against compose stack
	./build.sh smoke

clean: ## Remove dev images
	docker rmi \
		$(REGISTRY)/python-runner-base:$(TAG) \
		$(REGISTRY)/python-gpu-runner-base:$(TAG) \
		$(REGISTRY)/python-gpu-media-runner-base:$(TAG) \
		$(REGISTRY)/openai-chat-runner:$(TAG) \
		$(REGISTRY)/openai-embeddings-runner:$(TAG) \
		$(REGISTRY)/openai-audio-runner:$(TAG) \
		$(REGISTRY)/openai-tts-runner:$(TAG) \
		$(REGISTRY)/openai-image-generation-runner:$(TAG) \
		$(REGISTRY)/image-model-downloader:$(TAG) \
		$(REGISTRY)/openai-tester:$(TAG) \
		2>/dev/null || true

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
