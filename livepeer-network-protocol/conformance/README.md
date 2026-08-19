# Conformance suite — paid-job/v1 and paid-session/v1

Executable conformance for the v1 protocols. Every scenario pins a
normative clause from `../protocols/paid-job.md` (§7),
`../protocols/paid-session.md` (§10), or
`../protocols/runtime-descriptor.md` (§6). The suite is
implementation-independent: it never imports the reference broker and
speaks only the wire contract.

## Auto mode (default)

Runs against the in-repo reference broker:

```
make conformance
```

The runner builds and starts `capability-broker` with a generated host
config (mock payment daemon, temp state store), pointing at the suite's
own fake session runner and fake job backend, executes every scenario,
prints a PASS/FAIL report, and exits non-zero on any failure.

## URL mode (any broker implementation)

```
go run ./cmd/livepeer-conformance --broker-url http://your-broker:8080 --pause
```

Work units are configurable so you can run against your own offerings
without patching scenarios: `--job-unit` (default `tokens`) and
`--session-unit` (default `participant_minutes`). The units are properties
of your offerings, not of the protocols or the descriptor schemas —
`--session-unit participant_seconds` is as valid as the default.

The suite starts its fakes and prints their addresses. Configure your
broker to serve these offerings before pressing Enter:

| Capability / offering | Protocol | Declares | Backend |
|---|---|---|---|
| `conformance:job` / `all` | `paid-job/v1` | transports unary, stream, multipart; unit `tokens`; extractor equivalent to `openai-usage` | fake backend base URL |
| `conformance:job` / `unary-only` | `paid-job/v1` | transport unary only | fake backend base URL |
| `conformance:job` / `always-error` | `paid-job/v1` | transport unary | fake backend `/error` route |
| `conformance:session` / `default` | `paid-session/v1` | `descriptor_schema: sfu-room/v1`; unit `participant_minutes`; runner paths `/sessions`, `/sessions/{id}` | fake runner base URL |
| `conformance:session` / `fast-heartbeat` | `paid-session/v1` | same, but `heartbeat: {interval_seconds: 1, missed_threshold: 2}` | fake runner base URL |

The `fast-heartbeat` offering exists so liveness enforcement is observable in
seconds rather than minutes. Omit it and the heartbeat scenario skips rather
than failing.

The broker's externally-reachable base URL must be its callback source
(`external_base_url` or equivalent) so the fake runner's captured
callback coordinates land on the broker under test.

## Scenarios that need more than HTTP

Two scenarios need control over the implementation, not just its wire:

- **`paid-session/restart-rebind`** restarts the broker mid-session against a
  payment layer that survives, and **requires** the rebind branch — same
  `work_id`, same credential, surviving usage watermark, duplicate detection
  still correct, and top-up/end still working. A broker that goes terminal
  here is failing recovery, not choosing the other branch.
- **`paid-session/restart-terminal-when-unbillable`** restarts the broker with
  its own session store intact but the payment layer's state discarded — the
  "runner still has it, payment layer does not" case — and requires the
  terminal branch, with the forbidden outcomes (second `work_id`, runner left
  serving) checked there too.

Both restart scenarios run for real in **auto mode**, where the suite owns the
process. In URL mode they SKIP, because the suite cannot restart a broker it
did not start; demonstrate them with your own harness.

> **Why the payment layer matters here.** Auto mode configures the reference
> broker's in-process mock with `mock_state_path`, so its ledger survives the
> restart the way the real daemon's BoltDB store does. Without that, every
> restarted session takes the terminal branch and the rebind assertions —
> the operationally important half of §9.2 — never execute even though the
> suite passes. If you run in URL mode, make sure your payment layer outlives
> your broker restart before claiming rebind coverage.
- **`paid-session/heartbeat-enforcement`** opens against the
  `fast-heartbeat` offering, sends nothing, and waits for the broker to tear
  the session down — asserting the terminal reason is `heartbeat_lost`, that
  the *runner* was actually terminated (not merely a record flipped), and
  that a late `end` doesn't rewrite the close reason. It runs anywhere the
  `fast-heartbeat` offering is configured.
