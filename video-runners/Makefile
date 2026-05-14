# video-runners build + smoke.
# Per core belief #15: every gesture is a docker invocation; no host toolchain
# required.

REGISTRY    ?= tztcloud
TAG         ?= v1.0.0
CODECS_IMAGE ?= $(REGISTRY)/codecs-builder:$(TAG)

.PHONY: build codecs transcode abr tester smoke clean help

build: codecs transcode abr tester ## Build all images locally

codecs: ## Build the codecs-builder base image
	docker build \
		-t $(CODECS_IMAGE) \
		-f codecs-builder/Dockerfile \
		codecs-builder

transcode: codecs ## Build the transcode-runner image
	docker build \
		--build-arg CODECS_IMAGE=$(CODECS_IMAGE) \
		-t $(REGISTRY)/transcode-runner:$(TAG) \
		-f transcode-runner/Dockerfile \
		.

abr: codecs ## Build the abr-runner image
	docker build \
		--build-arg CODECS_IMAGE=$(CODECS_IMAGE) \
		-t $(REGISTRY)/abr-runner:$(TAG) \
		-f abr-runner/Dockerfile \
		.

tester: ## Build the transcode-tester image (Node integration harness)
	docker build \
		-t $(REGISTRY)/transcode-tester:$(TAG) \
		-f transcode-tester/Dockerfile \
		transcode-tester

smoke: ## Smoke against fixture mp4 (data/)
	./build.sh smoke

clean: ## Remove dev images
	docker rmi \
		$(CODECS_IMAGE) \
		$(REGISTRY)/transcode-runner:$(TAG) \
		$(REGISTRY)/abr-runner:$(TAG) \
		$(REGISTRY)/transcode-tester:$(TAG) \
		2>/dev/null || true

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
