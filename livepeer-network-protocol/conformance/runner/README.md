# runner/

Source for the `tztcloud/livepeer-conformance` Docker image.

**Status:** v0.1 scaffold. Flag parsing works; fixture loading and assertions
are TODO. The binary exits with code 2 ("not implemented") to make the state
unambiguous.

## Planned package layout

```
runner/
├── go.mod
├── cmd/livepeer-conformance/main.go    # entry point; flag parsing
└── internal/
    ├── runner/         # main loop: load fixtures → drive target → report
    ├── fixtures/       # YAML fixture loader + schema
    ├── targets/        # broker.go, gateway.go — adapters per target type
    ├── modes/          # one driver per mode (http-reqresp, http-stream, ...)
    ├── mockbackend/    # HTTP / WebSocket / RTMP mock backend the impl
    │                    dispatches to (used for side-effect assertions)
    ├── verify/         # header validation, error-code matching, etc.
    └── report/         # pass/fail formatting (text, JSON, JUnit XML)
```

The `internal/` layout is **planned**, not built. Subdirectories will appear
as code lands.

## Build (from parent `conformance/` directory)

```bash
make build         # builds tztcloud/livepeer-conformance:dev
docker run --rm tztcloud/livepeer-conformance:dev --version
```

## Adding a mode driver (when implementation begins)

When a new mode lands in `livepeer-network-protocol/modes/`:

1. Add `internal/modes/<mode-name>/driver.go` implementing the
   `internal/modes.Driver` interface (see `internal/modes/types.go`, TBD).
2. Register the driver in `cmd/livepeer-conformance/main.go`.
3. Land fixtures under `livepeer-network-protocol/conformance/fixtures/<mode-name>/`.
4. Update the conformance test matrix in this README.

## Why a Go binary in a Dockerfile

Per [core belief #15](../../../docs/design-docs/core-beliefs.md), every component
ships with a Docker-first build/run story. The runner is Go internally, but
implementers in any language run the published image via `docker run` (or
`make test`) — they never touch Go locally.
