# AGENTS.md

This is `vtuber-runner/` — the Python session-runtime workload binary +
browser-side avatar renderer for the Livepeer vtuber product. One
runner = one host. Each session spawns a headless Chromium child that
loads the vendored avatar-renderer dist; the Python service drives the
renderer over a control-WS, encodes audio + video, mux'es them, and
trickle-publishes upstream. Work-units (per-second) are reported over
the `SessionRunnerControl.ReportWorkUnits` gRPC bidi-stream to the
broker (see
[`../livepeer-network-protocol/proto/livepeer/sessionrunner/v1/control.proto`](../livepeer-network-protocol/proto/livepeer/sessionrunner/v1/control.proto)).

Component-local agent map. Repo-root [`../AGENTS.md`](../AGENTS.md) is
the cross-cutting map. The migration brief is plan
[0013-vtuber](../docs/exec-plans/active/0013-vtuber-suite-migration.md).

## Operating principles

Inherited from the repo root. Plus:

- **Python 3.12 + FastAPI + Pydantic + structlog** (variance documented
  in plan 0013-vtuber §6.3) — the runner orchestrates Chromium via
  Playwright + asyncio.subprocess, mux's audio with PyAV + numpy, and
  trickle-publishes via aiohttp + msgpack. A TS port is not viable.
- **Browser-side renderer is a sibling sub-workspace at
  `avatar-renderer/`** (TS + Vite + three.js + @pixiv/three-vrm +
  WebCodecs). Per OQ1 lock the renderer is *always* rebuilt from
  source in stage 1 of the runner Dockerfile; no pre-built artifact
  ships separately.
- **OLV (Open-LLM-VTuber) is vendored at `third_party/olv/`** per
  OQ2 lock — vendor lift, not a git submodule. See
  [`third_party/olv/UPSTREAM.md`](./third_party/olv/UPSTREAM.md) for
  upstream commit hash + rebase procedure.
- **Work-units flow over `SessionRunnerControl.ReportWorkUnits`** per
  OQ5 lock — runner reports monotonic deltas; broker accumulates into
  `atomic.Uint64`; broker's interim-debit ticker reads via
  `LiveCounter.CurrentUnits()`. **Not** response trailer; **not**
  control-WS events.
- **No "bridge" / "BYOC" terminology.** Source paths in citation
  strings preserve the historical names.

## Where to look

| Question | File |
|---|---|
| What is this component? | [`README.md`](./README.md) |
| Migration brief | [`../docs/exec-plans/active/0013-vtuber-suite-migration.md`](../docs/exec-plans/active/0013-vtuber-suite-migration.md) §4.3, §5.3 |
| Avatar renderer | [`avatar-renderer/`](./avatar-renderer/) |
| Vendored OLV | [`third_party/olv/`](./third_party/olv/) + [`third_party/olv/UPSTREAM.md`](./third_party/olv/UPSTREAM.md) |

## Layout

```
vtuber-runner/
  pyproject.toml          ← package name `session_runner` (Python)
  Dockerfile              ← 3-stage: Vite renderer build → uv deps → runtime + Playwright + chromium-headless-shell
  src/session_runner/
    runtime/              ← FastAPI app, task graph, entrypoint
    config/               ← SessionConfig
    types/                ← API + media + state Pydantic models
    ui/                   ← HTTP routes (POST /api/sessions/start etc.)
    service/              ← VTuberSession orchestrator, mux pipeline (PyAV), transports, control plane
    providers/            ← telemetry, Playwright launcher, session-scoped-bearer HTTP client, LLM/TTS, trickle
  avatar-renderer/        ← TS sibling sub-workspace (browser code)
  third_party/olv/        ← vendored upstream (no submodule); UPSTREAM.md documents rebase
  tests/
    unit/
    integration/
    fixtures/             ← test VRM
```

## Source attribution

Code is ported from `livepeer-vtuber-project/session-runner/` (Python +
tests) and `livepeer-vtuber-project/avatar-renderer/` (browser TS) per
plan 0013-vtuber §5.3. The vendored OLV upstream is copied from
`livepeer-vtuber-project/session-runner/third_party/olv/` verbatim;
upstream commit hash is recorded in
[`third_party/olv/UPSTREAM.md`](./third_party/olv/UPSTREAM.md).
