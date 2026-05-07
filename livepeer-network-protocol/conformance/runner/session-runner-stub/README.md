# Conformance stub session-runner

Stub implementation of the SessionRunnerControl + SessionRunnerMedia gRPC
services from
[`../../../proto/livepeer/sessionrunner/v1/`](../../../proto/livepeer/sessionrunner/v1/).
Used by the conformance fixtures under
[`../../fixtures/session-control-plus-media/`](../../fixtures/session-control-plus-media/)
to exercise the broker's session-runner subprocess lifecycle path
without dragging in the production vtuber-session image.

Q6 lock: this stub lives here because operators read the conformance
README first; the stub belongs beside the fixtures it serves.

## Behaviors

The stub picks its behavior from the `LIVEPEER_STUB_BEHAVIOR` env var:

| Behavior | Effect |
|---|---|
| `echo` (default) | echoes every workload envelope back as `<type>.echo` |
| `burst` | emits envelopes at `LIVEPEER_STUB_BURST_RATE_HZ` until the broker stops reading |
| `tick` | emits a `session.usage.tick` every `LIVEPEER_STUB_TICK_INTERVAL_MS` |
| `crash-after-startup` | sleeps `LIVEPEER_STUB_CRASH_DELAY_MS`, then `os.Exit(1)` |

`LIVEPEER_SESSION_RUNNER_SOCK` (set by the broker on launch) is the unix
socket path the stub binds. Frames service mirrors any inbound RTP back
as egress for end-to-end media tests.

## Build

```
docker build \
  -f livepeer-network-protocol/conformance/runner/session-runner-stub/Dockerfile \
  -t tztcloud/livepeer-conformance-session-runner:dev \
  .
```

(Build from the monorepo root — the build context contains both the
runner source and the proto-go module the stub imports.)
