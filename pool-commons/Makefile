.PHONY: test build shell
IMAGE ?= tztcloud/livepeer-pool-commons:dev

test:
	docker build --target test -t $(IMAGE)-test -f Dockerfile ..
build:
	docker build --target build -t $(IMAGE) -f Dockerfile ..
shell:
	docker run --rm -it -v $(CURDIR):/src -w /src golang:1.25.7 sh
