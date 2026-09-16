IMAGE ?= livepeer-member-portal:local
.PHONY: build test run
build:
	docker build -f Dockerfile -t $(IMAGE) ..
test:
	docker build -f Dockerfile --target test ..
run: build
	docker run --rm --user "$$(id -u):$$(id -g)" -v "$(CURDIR)/local:/etc/member-portal:ro" -v "$(CURDIR)/data:/var/lib/member-portal" -p 127.0.0.1:8090:8090 $(IMAGE)

.PHONY: test-browser
test-browser:
	./browser/run.sh
