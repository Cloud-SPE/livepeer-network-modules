# Custom lints

Beyond `golangci-lint`, this repo ships small custom analyzers for invariants depguard / staticcheck don't cleanly express. They're plain Go programs runnable with `go run`.

## doc-gardener

Checks the `docs/` directory:
- Every design-doc has frontmatter (`title`, `status`, `last-reviewed`).
- `status` is one of `proposed | accepted | verified | deprecated`.
- `last-reviewed` is RFC3339 and at most 365 days old.
- Cross-links between docs resolve to existing files.
- Every link from `AGENTS.md` / `DESIGN.md` / `README.md` resolves.

Run: `go run ./lint/doc-gardener --root .`

## no-unverified-manifest

Checks Go source: any function that produces or returns a `*types.Manifest` from a wire-bytes input must reach the manifest through `internal/types.DecodeManifest` (the only boundary-validating decoder). Code paths that JSON-unmarshal directly into a `*types.Manifest` are flagged with a remediation message pointing at `DecodeManifest`.

Run: `go run ./lint/no-unverified-manifest --root .`

## layer-check (planned)

Status: stub. golangci-lint's `depguard` covers v1; the richer analyzer is planned in a follow-up exec-plan. See `docs/exec-plans/tech-debt-tracker.md`.

## coverage-gate (planned)

Status: stub. CI runs `go test -race -coverprofile=coverage.out` and emits a coverage file; gate enforcement (≥75% per package) is planned. See `docs/exec-plans/tech-debt-tracker.md`.
