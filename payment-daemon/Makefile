# payment-daemon build + run + test.
# Per core belief #15: every gesture is a docker invocation; no host
# toolchain required at usage time. (`make proto` does require protoc on
# the host, since codegen is a developer-time step.)

IMAGE   ?= tztcloud/livepeer-payment-daemon
TAG     ?= dev
VERSION := $(shell cat ../livepeer-network-protocol/VERSION 2>/dev/null || echo dev)

PROTO_SRC := ../livepeer-network-protocol/proto
PROTO_OUT := internal/proto

.PHONY: build run dev test proto publish clean help

build: ## Build dev image locally
	docker build \
		--target builder \
		--build-arg VERSION=$(VERSION) \
		-t $(IMAGE):$(TAG)-builder \
		-f Dockerfile \
		.. \
	&& docker build \
		--build-arg VERSION=$(VERSION) \
		-t $(IMAGE):$(TAG) \
		-f Dockerfile \
		..

run: ## Run the daemon locally; sock + db live under ./run/
	mkdir -p run
	docker run --rm \
		-v $(PWD)/run:/srv \
		$(IMAGE):$(TAG) \
		--socket /srv/payment-daemon.sock \
		--db /srv/sessions.db

dev: build run ## Build + run in one shot

test: ## Run go test ./... inside the test stage
	docker build \
		--target test \
		-t $(IMAGE):$(TAG)-test \
		-f Dockerfile \
		..

proto: ## Regenerate Go bindings from the proto sources (requires protoc on host)
	protoc \
		-I=$(PROTO_SRC) \
		--go_out=paths=source_relative:$(PROTO_OUT) \
		--go-grpc_out=paths=source_relative:$(PROTO_OUT) \
		$(PROTO_SRC)/livepeer/payments/v1/payment.proto \
		$(PROTO_SRC)/livepeer/payments/v1/payee_daemon.proto

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
	docker rmi $(IMAGE):$(TAG)-builder 2>/dev/null || true
	docker rmi $(IMAGE):$(TAG)-test 2>/dev/null || true
	rm -rf run/

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
