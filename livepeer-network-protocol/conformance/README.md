# conformance/

Test suite for verifying broker and gateway implementations conform to the spec.

**Status:** scaffold TBD per [plan 0002](../../docs/exec-plans/active/0002-define-interaction-modes-spec.md).

Layout (planned):

- `fixtures/` — declarative YAML test cases, one folder per mode.
- `runner/` — Go runner source (`cmd/livepeer-conformance`, `internal/`, `Dockerfile`).
- `compose.yaml` — full self-test stack (runner + mock-backend + sample broker).
- `Makefile` — wraps `build`, `test`, `shell`, `publish`.

Distribution: Docker image at `tztcloud/livepeer-conformance:<tag>`. Tag matches
the spec-wide [`../VERSION`](../VERSION).

Implementers run the image against their broker or gateway:

```bash
docker run --rm \
  --add-host=host.docker.internal:host-gateway \
  tztcloud/livepeer-conformance:<tag> \
  --target broker \
  --url http://host.docker.internal:8080
```

No local Go install required (per [core belief #15](../../docs/design-docs/core-beliefs.md)).
