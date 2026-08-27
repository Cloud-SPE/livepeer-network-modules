# AGENTS.md

This is `pool-member-agent/` — the host-side process shipped in the pool signup
bundle. Since plan 0044 the bundle ships **only** this binary: the agent starts
the workload containers itself.

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

## The second job: desired state

`internal/desiredstate` + `cmd/pool-member-agent/desiredloop.go` fetch what this
host should be running and make it so. It starts only when all of
`POOL_CONTROLLER_URL`, `POOL_ENROLLMENT_ID` and an enrollment token are set;
without them the agent does the attach half only, against locally declared
runners.

- **The agent holds no policy.** Which template, which GPU, when to withdraw
  one — all decided by `pool-controller`. An agent that disagrees with the
  controller is a bug in the agent, not a fallback to be added.
- **Never make a control-plane outage a data-plane one.** A failed reconcile is
  logged and retried; the host keeps running exactly what it was running. Do
  not add a path that tears containers down because the controller is
  unreachable.
- **Drain before stop, always.** A service leaving desired state is marked
  `draining` in the attach document and the broker is told *before* the
  container goes away (`runner-attach` §7.1). A draining service is still
  rendered into the compose file. Reordering these is how in-flight requests
  get dropped on the floor.
- **`runnerState` is the seam between the two loops.** The desired-state loop
  writes it; the tunnel loop reads it on every register and re-register, and
  wakes on its `changed` channel. Never build an attach document from the
  config the process booted with — a pool-managed host would then re-send its
  boot-time runner set forever.
- **The compose file is written whole and renamed.** A half-written file caught
  by a concurrent `docker compose up` is a host that stops serving for reasons
  nobody can reconstruct.
- **Rotation must not lose the token.** The controller returns the replacement
  exactly once. Write the new one to a temp file, rename, and leave the old one
  readable until the new one is safely down.
- `desiredstate.Runner` is an interface so reconcile logic is testable without
  a docker daemon. Keep it that way; a dry run should be a real mode, not a
  code path nobody exercises.

## Build / Test

- `make check` (`go test ./...` plus the attach-document schema check)
- `make build`
