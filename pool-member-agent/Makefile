.PHONY: build test check check-attach-docs

build:
	go build ./cmd/pool-member-agent

test:
	go test ./...

# The goldens under testdata/attach are what this agent actually puts on
# the wire. Validating them against the protocol module's own schema is
# how a profile change that drifts from the contract fails here rather
# than at a member's first attach.
check-attach-docs:
	python3 ../livepeer-network-protocol/scripts/validate-doc.py \
	  --schema protocols/runner-attach/schema.json \
	  testdata/attach/*.json

check: test check-attach-docs
