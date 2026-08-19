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
| `conformance:session` / `fast-heartbeat` | `paid-session/v1` | same, but `heartbeat: {interval_seconds: 1, missed_threshold: 2}` and a fixed lease | fake runner base URL |
| `conformance:job` / `slow` | `paid-job/v1` | transport unary | fake backend `/slow` route (~3s) |
| `conformance:job` / `longstream` | `paid-job/v1` | transport stream | fake backend `/longstream` route (~6s of SSE) |
| `conformance:session` / `bounded-refill` | `paid-session/v1` | `refill: bounded`, fixed lease | fake runner base URL |
| `conformance:session` / `short-lease` | `paid-session/v1` | `lease_policy: fixed`, `lease_max_seconds: 1`, heartbeat far away | fake runner base URL |
| `conformance:session` / `rtmp-hls` | `paid-session/v1` | `descriptor_schema: rtmp-hls/v1` | fake runner base URL |
| `conformance:session` / `scope-passthrough` | `paid-session/v1` | `descriptor_schema: scope-passthrough/v1` | fake runner base URL |
| `conformance:session` / `trickle-egress` | `paid-session/v1` | `descriptor_schema: trickle-egress/v1` | fake runner base URL |

Offerings you do not serve simply cause their scenarios to SKIP rather than
fail — the per-schema fixtures and the optional control-WS binding are the
cases where that is expected.

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

## What this suite does *not* cover

A green run is not a claim of total coverage. Two assertions in the specs'
own Conformance sections are **not** executable from outside an
implementation, and one property is only as strong as your configuration.
An implementer citing this suite as protocol evidence should know exactly
where its edges are.

**1. Exactly-once debit under a transient payment failure.**
`paid-job/v1` §7 and `paid-session/v1` §10 both call for this to be
"verified by fault injection on a transiently failing debit." The suite
cannot inject a failure into an implementation's payment layer — there is
no wire surface for it, by design. What the suite *does* prove is the
observable half: duplicate and reordered events are safe, and a retried
request id converges on the recorded outcome without a second backend
execution.

*What to demonstrate instead:* an implementation-side test that fails a
debit transiently, retries the same event, and asserts exactly one charge —
never zero (acknowledged but uncharged), never two. The reference broker's
is `TestExactlyOnceDebitUnderRetry` in `capability-broker/internal/sessionengine`.

**2. "Payment state closed" on a fail-closed open.**
`runtime-descriptor/v1` §6 requires that a rejected descriptor leaves
payment state closed. The suite asserts the visible half — the open fails
and the runner session is terminated — but after a failed open there is no
session for the gateway to query, so the payment side is not observable
black-box.

*What to demonstrate instead:* an implementation-side assertion that the
payee session was closed on every fail-closed open path.

**3. Restart branch coverage depends on your payment layer, not your broker.**
See the note above: if your payment layer does not outlive a broker
restart, `paid-session/restart-rebind` will fail and every restarted
session will take the terminal branch. That is a correct result, not a
suite defect — but it means "the suite passes" carries a different meaning
depending on how you deploy. Run it both ways if both are realistic for
your operators.

**4. Payment validity is mocked.** Auto mode runs the reference broker
against a mock payment client, so nothing here exercises real ticket
validation, real balance arithmetic, or the payee daemon's own idempotency.
The suite tests the *protocol's* handling of payment outcomes, not the
payment layer itself.
