.PHONY: validate-examples conformance verify proto proto-check

# Guard against a schema change leaving its own examples invalid.
validate-examples:
	python3 scripts/validate-examples.py

verify:
	go -C verify test ./...

conformance:
	$(MAKE) -C conformance conformance

check: validate-examples verify

# ---------------------------------------------------------------------------
# Code generation
#
# ALWAYS regenerate through this target, never with a protoc-gen-go found on
# PATH. Distro packages still ship the legacy APIv1 plugin (the one that emits
# `github.com/golang/protobuf` imports and `proto.ProtoPackageIsVersion3`), and
# it does not fail — it rewrites every generated file into the old API. That
# looks like an enormous unrelated diff on top of whatever field you meant to
# add, and it compiles.
#
# The protobuf plugin is built from proto-go's own go.mod, so its version is
# whatever that module already depends on and no network is needed. The gRPC
# plugin lives in a separate module and is pinned here to the version the
# checked-in *_grpc.pb.go files declare.

PROTO_MODULE := github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol
PROTO_FILES  := $(wildcard proto/livepeer/payments/v1/*.proto)
PROTO_BIN    := $(CURDIR)/.protoc-bin
GRPC_PLUGIN_VERSION := v1.6.2

$(PROTO_BIN)/protoc-gen-go:
	@mkdir -p $(PROTO_BIN)
	go -C proto-go build -o $(PROTO_BIN)/protoc-gen-go google.golang.org/protobuf/cmd/protoc-gen-go

$(PROTO_BIN)/protoc-gen-go-grpc:
	@mkdir -p $(PROTO_BIN)
	GOBIN=$(PROTO_BIN) go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@$(GRPC_PLUGIN_VERSION)

proto: $(PROTO_BIN)/protoc-gen-go $(PROTO_BIN)/protoc-gen-go-grpc
	protoc \
	  --plugin=protoc-gen-go=$(PROTO_BIN)/protoc-gen-go \
	  --plugin=protoc-gen-go-grpc=$(PROTO_BIN)/protoc-gen-go-grpc \
	  --proto_path=proto \
	  --go_out=. --go_opt=module=$(PROTO_MODULE) \
	  --go-grpc_out=. --go-grpc_opt=module=$(PROTO_MODULE) \
	  $(PROTO_FILES)
	go -C proto-go build ./...

# Fails when the checked-in bindings do not match the .proto files — a
# hand-edited .pb.go, or a .proto change someone forgot to regenerate.
proto-check: proto
	@if ! git diff --quiet -- proto-go; then \
	  echo "proto-go is out of date with proto/ — commit the result of 'make proto':"; \
	  git --no-pager diff --stat -- proto-go; \
	  exit 1; \
	fi
	@echo "proto-go matches proto/"
