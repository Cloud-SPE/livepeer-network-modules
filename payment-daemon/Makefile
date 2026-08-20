# payment-daemon build + run + test.
# Per core belief #15: every gesture is a docker invocation; no host
# toolchain required at usage time. (`make proto` does require protoc on
# the host, since codegen is a developer-time step.)

IMAGE   ?= tztcloud/livepeer-payment-daemon
TAG     ?= dev
MODE    ?= receiver
VERSION ?= $(shell VERSION_PREFIX="$(TAG)" FALLBACK_VERSION=dev ../infra/build/git-version.sh)

PROTO_SRC := ../livepeer-network-protocol/proto
PROTO_OUT := ../livepeer-network-protocol/proto-go

.PHONY: build run dev test lint proto sim publish clean help

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

run: ## Run the daemon locally (MODE=receiver|sender); sock + db live under ./run/
	mkdir -p run
	docker run --rm \
		-v $(PWD)/run:/srv \
		$(IMAGE):$(TAG) \
		--mode $(MODE) \
		--socket /srv/payment-daemon.sock \
		--db /srv/sessions.db

dev: build run ## Build + run in one shot

test: ## Run go test ./... inside the test stage (includes the no-secrets-in-logs lint via TestRunPaymentDaemonTreeIsClean)
	docker build \
		--target test \
		-t $(IMAGE):$(TAG)-test \
		-f Dockerfile \
		..

lint: ## Run the no-secrets-in-logs lint program against the payment-daemon tree (covered by `make test`; this target is for ad-hoc invocation)
	docker run --rm \
		-v $(PWD)/..:/src \
		-w /src/payment-daemon \
		golang:1.25-alpine \
		sh -c 'apk add --no-cache git >/dev/null 2>&1 && go run ./lint/no-secrets-in-logs --root .'

proto: ## Regenerate Go bindings from the proto sources (requires protoc on host)
	protoc \
		-I=$(PROTO_SRC) \
		--go_out=paths=source_relative:$(PROTO_OUT) \
		--go-grpc_out=paths=source_relative:$(PROTO_OUT) \
	$(PROTO_SRC)/livepeer/payments/v1/types.proto \
	$(PROTO_SRC)/livepeer/payments/v1/payee_admin.proto \
	$(PROTO_SRC)/livepeer/payments/v1/payee_daemon.proto \
	$(PROTO_SRC)/livepeer/payments/v1/payer_daemon.proto

sim: ## Run the payout simulator (SCENARIO=payment-daemon/scenarios/openai-chat-qwen3-8b.yaml FORMAT=text)
	@if [ -z "$(SCENARIO)" ]; then \
		echo "set SCENARIO=path/to/scenario.yaml"; \
		exit 1; \
	fi
	go run ./cmd/payout-sim --scenario $(SCENARIO) --format $${FORMAT:-text}

publish: ## Build + push multi-arch image (requires TAG set to a real version)
	@if [ "$(TAG)" = "dev" ]; then \
		echo "set TAG to a real version before publishing (e.g. make publish TAG=0.1.0)"; \
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
	docker rmi $(IMAGE):$(TAG)-builder 2>/dev/null || true
	docker rmi $(IMAGE):$(TAG)-test 2>/dev/null || true
	rm -rf run/

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
