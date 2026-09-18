IMAGE ?= livepeer-orchestrator-setup:local
ROOT := $(abspath ..)
DOCKER_SOCKET ?= $(patsubst unix://%,%,$(if $(DOCKER_HOST),$(DOCKER_HOST),$(shell docker context inspect --format '{{.Endpoints.docker.Host}}')))
.PHONY: build test acceptance shell publish
build:
	docker build -f Dockerfile -t $(IMAGE) $(ROOT)
test: build
	docker run --rm --network none --read-only --tmpfs /tmp -v $(CURDIR):/src:ro --entrypoint python3 $(IMAGE) -m unittest discover -s /src/tests -p 'test_*.py' -v
acceptance: build
	docker run --rm --network none --mount type=bind,src=$(DOCKER_SOCKET),dst=/var/run/docker.sock -v /tmp:/tmp -v $(CURDIR):/src:ro --entrypoint python3 $(IMAGE) /src/tests/container_acceptance.py --image $(IMAGE)
shell: build
	docker run --rm -it --network none --entrypoint sh $(IMAGE)
publish:
	@test -n "$(PUBLISH_IMAGE)" || (echo 'Set PUBLISH_IMAGE explicitly'; exit 1)
	docker tag $(IMAGE) $(PUBLISH_IMAGE)
	docker push $(PUBLISH_IMAGE)
