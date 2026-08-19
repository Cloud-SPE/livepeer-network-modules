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

The suite starts its fakes and prints their addresses. Configure your
broker to serve these offerings before pressing Enter:

| Capability / offering | Protocol | Declares | Backend |
|---|---|---|---|
| `conformance:job` / `all` | `paid-job/v1` | transports unary, stream, multipart; unit `tokens`; extractor equivalent to `openai-usage` | fake backend base URL |
| `conformance:job` / `unary-only` | `paid-job/v1` | transport unary only | fake backend base URL |
| `conformance:job` / `always-error` | `paid-job/v1` | transport unary | fake backend `/error` route |
| `conformance:session` / `default` | `paid-session/v1` | `descriptor_schema: sfu-room/v1`; unit `participant_minutes`; runner paths `/sessions`, `/sessions/{id}` | fake runner base URL |

The broker's externally-reachable base URL must be its callback source
(`external_base_url` or equivalent) so the fake runner's captured
callback coordinates land on the broker under test.

## Skipped scenarios

`paid-session/restart-rebind` and `paid-session/heartbeat-enforcement`
need broker process/clock control that a black-box wire suite cannot
exercise portably; they are reported SKIP with pointers to the
reference broker's engine tests that cover them
(`capability-broker/internal/sessionengine`). An implementation other
than the reference broker must demonstrate them with its own tests.
