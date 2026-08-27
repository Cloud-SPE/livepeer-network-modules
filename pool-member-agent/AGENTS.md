# AGENTS.md

This is `pool-member-agent/` — the lightweight host-side process shipped in the
pool signup bundle.

## Scope

- Keep the member workflow simple: configure by environment, run under Docker
  Compose, make outbound-only calls.
- The agent must not run `capability-broker` or `payment-daemon`.
- Hardware inventory comes from the host runtime (`nvidia-smi` for NVIDIA GPUs)
  and rides the attach document, not a controller report.
- The agent attaches outbound and declares what the host runs
  (`../livepeer-network-protocol/protocols/runner-attach.md`). The broker
  remains the payment and routing authority.
- **Adapter profiles are where runner facts live** (`internal/attach`). Adding a
  workload means adding a profile here, not asking an operator to transcribe
  paths, transports, and extractors into broker config.
- **The goldens are the contract test.** `make check-attach-docs` validates
  `testdata/attach/*.json` against the protocol schema; the broker's test suite
  runs the same files through its own validator. Regenerate with
  `UPDATE_GOLDEN=1 go test ./internal/attach/` and re-run both.

## Build / Test

- `make check` (`go test ./...` plus the attach-document schema check)
- `make build`
