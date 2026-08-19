.PHONY: validate-examples conformance verify

# Guard against a schema change leaving its own examples invalid.
validate-examples:
	python3 scripts/validate-examples.py

verify:
	go -C verify test ./...

conformance:
	$(MAKE) -C conformance conformance

check: validate-examples verify
