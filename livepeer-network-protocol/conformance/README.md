# conformance/

Test suite for verifying broker and gateway implementations conform to the spec.

**Status:** v0.1 scaffold landed 2026-05-06. Build + run gestures work; fixture
loading and assertion logic in the runner are TODO.

## Files

| Path | What it is |
|---|---|
| [`Dockerfile`](./Dockerfile) | Multi-stage build for the runner image. Distroless final stage; fixtures baked in. |
| [`Makefile`](./Makefile) | `make build / test / test-compose / publish / clean / help`. Per core belief #15 — no host toolchain required. |
| [`compose.yaml`](./compose.yaml) | Self-test stack (runner + mock-backend + sample broker). Sample broker is commented out until plan 0003 ships `tztcloud/livepeer-capability-broker:dev`. |
| [`runner/`](./runner/) | Go source for the runner binary. Currently a flag-parsing stub; fixture loading + assertions are TODO. |
| [`fixtures/`](./fixtures/) | Per-mode fixture folders (currently empty). Format documented in [`fixtures/README.md`](./fixtures/README.md). |

## Distribution

Docker image at `tztcloud/livepeer-conformance:<tag>`. Tag matches the spec-wide
[`../VERSION`](../VERSION).

Implementers run the image against their broker or gateway:

```bash
docker run --rm \
  --add-host=host.docker.internal:host-gateway \
  tztcloud/livepeer-conformance:<tag> \
  --target broker \
  --url http://host.docker.internal:8080
```

No local Go install required (per [core belief #15](../../docs/design-docs/core-beliefs.md)).

## Running locally during dev

```bash
make build                                    # build dev image
make test BROKER_URL=http://host.docker.internal:8080
make test MODES=http-reqresp,http-stream BROKER_URL=...
make test-compose                              # full self-test stack
```

## What the runner currently does

The v0.1 binary parses flags and prints a TODO message, then exits with code 2
("not implemented"). This makes it unambiguous that the scaffold is in place
but the test logic is not. Fixture loading and assertions follow as the mode
drivers are written — see [`runner/README.md`](./runner/README.md) for the
planned package layout.
