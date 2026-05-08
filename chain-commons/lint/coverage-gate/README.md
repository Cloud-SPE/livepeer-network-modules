# coverage-gate

Per-package coverage threshold enforcement for `chain-commons`.

The full implementation (a custom Go tool that parses `coverage.out` and exits
non-zero on any package below threshold) lands in plan 0001 §J — this directory
holds the `exemptions.txt` contract now so subsequent commits can land the tool
against a known list.

## How it will work (target shape)

```sh
go test -coverprofile=coverage.out ./...
go run ./lint/coverage-gate -threshold=75 -exemptions=lint/coverage-gate/exemptions.txt
```

Reads `coverage.out`, computes per-package coverage, and exits 1 if any
non-exempt package is below 75%.

## Adding an exemption

Append a line to `exemptions.txt`:

```
<package-import-path>    # written reason
```

The reason is mandatory and reviewed at PR time. Reasons usually point at
integration tests, hardware dependencies, or interface-only packages.

## Removing an exemption

Delete the line. CI will then enforce the global threshold against the package.
Coverage gate ratchets only up — exemptions added must be permanently
justified, not "we'll add tests later."
