# Conformance suite — paid-job/v1 and paid-session/v1

Executable conformance for the v1 protocols. Every scenario pins a
normative clause from `../protocols/paid-job.md` (§7),
`../protocols/paid-session.md` (§10),
`../protocols/runtime-descriptor.md` (§6), or
`../protocols/runner-attach.md` (§9). The suite is
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
| `conformance:job` / `fractional` | `paid-job/v1` | transport unary; **`price: {amount_wei: "100", per_units: 1000}`** | fake backend base URL |
| `conformance:session` / `bounded-refill` | `paid-session/v1` | `refill: bounded`, fixed lease | fake runner base URL |
| `conformance:session` / `short-lease` | `paid-session/v1` | `lease_policy: fixed`, `lease_max_seconds: 1`, heartbeat far away | fake runner base URL |
| `conformance:session` / `rtmp-hls` | `paid-session/v1` | `descriptor_schema: rtmp-hls/v1` | fake runner base URL |
| `conformance:session` / `scope-passthrough` | `paid-session/v1` | `descriptor_schema: scope-passthrough/v1` | fake runner base URL |
| `conformance:session` / `trickle-egress` | `paid-session/v1` | `descriptor_schema: trickle-egress/v1` | fake runner base URL |

The `fractional` offering is priced per *many* units on purpose. Every other
fixture is `per_units: 1` — the one denominator at which flooring and
ceiling agree — so a rounding defect cannot surface against them. Serve it
with a remainder-producing price and the paid path gets exercised where the
arithmetic actually differs.

Offerings you do not serve simply cause their scenarios to SKIP rather than
fail — the per-schema fixtures and the optional control-WS binding are the
cases where that is expected.

The `fast-heartbeat` offering exists so liveness enforcement is observable in
seconds rather than minutes. Omit it and the heartbeat scenario skips rather
than failing.

The broker's externally-reachable base URL must be its callback source
(`external_base_url` or equivalent) so the fake runner's captured
callback coordinates land on the broker under test.

### Reaching the fakes from another host or container

By default the fakes bind loopback, which only works when the broker under
test shares the host. When it does not — the published image against a
broker on a docker network, or a broker on another machine — bind and
advertise them explicitly:

| Flag | Default | Purpose |
|---|---|---|
| `--fakes-listen` | `127.0.0.1` | interface the fakes bind; `0.0.0.0` to accept off-host traffic |
| `--fakes-advertise` | `--fakes-listen`, or this host's name when binding `0.0.0.0` | host the broker under test uses to reach the fakes |
| `--fakes-backend-port` | `0` (ephemeral) | pin the fake job backend's port |
| `--fakes-runner-port` | `0` (ephemeral) | pin the fake session runner's port |

Pin the ports when the runner that serves the offerings has to name the
fakes before the suite starts — which is the usual case, since the runner's
declared endpoints are written up front:

```
docker run --rm --network lpm_default --name conformance \
    tztcloud/livepeer-conformance:<tag> \
    --broker-url http://broker:8080 \
    --fakes-listen 0.0.0.0 --fakes-advertise conformance \
    --fakes-backend-port 8091 --fakes-runner-port 8092 --warmup 20s
```

The runner behind the broker's offerings then points at
`http://conformance:8091` (job backend) and `http://conformance:8092`
(session runner). Host networking is no longer required.

`examples/docker-network/` holds a compose file and a reference-broker
config for exactly this shape. Note what URL mode does **not** do: it never
attaches a serving runner. The broker under test advertises nothing until
one of yours enrols and certifies, so that example needs a runner service of
your own alongside it. Auto mode is the self-contained path — it generates
the config and attaches the suite's own runner.

Two things bite a broker that came up before the runner behind it did:

- **`--warmup`.** A broker whose runner has not attached and certified yet
  advertises nothing, so the first scenarios get `503`. `--warmup 20s` gives
  the attach and its certification steps time to land on the now-live fakes.
  Not needed when the broker starts after the fakes, or when you use
  `--pause`.
- **Certify against the base URL, not the special routes.** The
  `always-error`, `slow`, and `longstream` offerings are served by runner
  entries whose declared endpoint is a route that 500s or answers slowly *on
  purpose*. Their runner's readiness recipe must point at the fake backend's
  base URL — a readiness step aimed at the route itself never certifies, and
  the scenarios fail with `503` instead of the behaviour under test.

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

## Runner-attach scenarios

The `attach/*` scenarios play the runner side of
[`../protocols/runner-attach.md`](../protocols/runner-attach.md): they open
the broker's WebSocket attach endpoint (`/internal/v1/worker/session`),
send a `register` frame carrying an attach document, and check the
`register_result`. They need an enrolled credential:

```
go run ./cmd/livepeer-conformance --broker-url http://your-broker:8080 \
    --attach-credential lpc_… --attach-host-id conformance-runner
```

Without `--attach-credential`, or against a broker that does not serve
the endpoint, they **skip** with the reason rather than fail — a broker
that predates plan 0043 is out of scope, not in violation. The scenarios
that also need a matched offer and the admin API (`replaces-on-resend`,
`never-mutates-offer`, `routes-by-local-id`) live with the broker-admin
fixtures and are not in this suite yet.
