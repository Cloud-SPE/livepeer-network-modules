# AGENTS.md

This is `vtuber-pipeline/` — the Python SaaS pipeline for the Livepeer
vtuber product. Three console-script binaries: `pipeline-mock-youtube`
(chat source), `pipeline-streams` (session orchestrator), `pipeline-egress`
(RTMP push worker). Pipeline calls `vtuber-gateway/` as a single
meta-customer (one shared API key per deployment); pipeline bills its
own customers internally.

Component-local agent map. Repo-root [`../AGENTS.md`](../AGENTS.md) is
the cross-cutting map. The migration brief is plan
[0013-vtuber](../docs/exec-plans/completed/0013-vtuber-suite-migration.md).

## Operating principles

Inherited from the repo root. Plus:

- **Python 3.12 + FastAPI + Pydantic + structlog.** Variance from the
  canonical TS lock is documented in plan 0013-vtuber §6.2. Pipeline
  is Python because the upstream pipeline-app is Python and a TS port
  buys nothing.
- **Pipeline is a meta-customer of `vtuber-gateway/`.** Per OQ4 lock:
  one shared `LIVEPEER_VTUBER_GATEWAY_API_KEY` per deployment;
  pipeline-app's customers never see vtuber-gateway directly.
- **No "bridge" terminology anywhere.** Symbols are `HTTPGatewayClient`,
  `GatewayClient`, `GatewayError`, `GatewaySessionOpenResult`,
  `gateway_session_id`. Env-var names are `STREAMS_GATEWAY_*`. The
  historical "bridge" name survives only in suite-citation paths.

## Where to look

| Question | File |
|---|---|
| What is this component? | [`README.md`](./README.md) |
| What's the design? | [`DESIGN.md`](./DESIGN.md) |
| Migration brief | [`../docs/exec-plans/completed/0013-vtuber-suite-migration.md`](../docs/exec-plans/completed/0013-vtuber-suite-migration.md) §4.2, §5.2 |

## Layout

```
vtuber-pipeline/
  pyproject.toml          ← package name `vtuber-pipeline`
  Dockerfile              ← bundles all three console scripts
  compose.yaml
  src/vtuber_pipeline/
    mock_youtube/         ← chat-source provider
    streams/              ← streams orchestrator (calls vtuber-gateway)
    egress/               ← ffmpeg RTMP push
  tests/
```

Each binary has its own `runtime/entrypoint.py` (ASGI app + uvicorn).

## Source attribution

Code in this component is ported from
`livepeer-vtuber-project/pipeline-app/src/pipeline/` — see
plan 0013-vtuber §5.2 for the source-to-destination map. The Python
package was renamed `pipeline` → `vtuber_pipeline` for namespace
clarity in the monorepo. The streams provider file `bridge.py` was
renamed to `gateway.py` and all `Bridge*` symbols were renamed to
`Gateway*` per the no-"bridge" convention.
