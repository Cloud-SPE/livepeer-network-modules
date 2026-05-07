.PHONY: build test lint smoke clean

build:
	pnpm -F @livepeer-rewrite/customer-portal build

test:
	pnpm -F @livepeer-rewrite/customer-portal test

lint:
	pnpm -F @livepeer-rewrite/customer-portal lint

smoke:
	docker compose -f compose.yaml up --abort-on-container-exit --exit-code-from smoke

clean:
	rm -rf dist node_modules
